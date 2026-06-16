// unfold-tsindexer: the TypeScript indexing engine, run as a sidecar by
// the Go process. It speaks newline-delimited JSON-RPC over stdio and
// emits the same Frame JSON shape the Go engine produces (see
// internal/model). One request per line in on stdin, one response per line
// out on stdout. All logging goes to stderr so it never corrupts the
// protocol.
//
// Methods: load, lookupSymbol, frame, frameForCall, search.
//
// Direct-call resolution and interface dispatch (candidates) cover .ts.
// Angular HTML templates (inline `template:` and external `templateUrl:`)
// are also indexed: each becomes a synthetic Frame whose source is the HTML
// and whose call sites point at the component's methods, so you can follow
// flow from a template into the component body.

import { createInterface } from "node:readline";
import { readFileSync } from "node:fs";
import { dirname, resolve as resolvePath } from "node:path";
import {
  ImplicitReceiver,
  parseTemplate,
  RecursiveAstVisitor,
  tmplAstVisitAll,
  TmplAstRecursiveVisitor,
  type AST,
} from "@angular/compiler";
import {
  Node,
  Project,
  SyntaxKind,
  type CallExpression,
  type ClassDeclaration,
  type Node as TNode,
  type Type as TsType,
} from "ts-morph";

// ---- wire types (mirror internal/model) ----

type CallKind = "direct" | "interface" | "indirect" | "fanout";

interface Receiver {
  targetId: string;
  label: string;
  provenance?: string;
  confidence?: string;
}

interface CallSite {
  id: string;
  spanStart: number;
  spanEnd: number;
  displayName: string;
  kind: CallKind;
  targetId?: string;
  candidates?: { targetId: string; label: string }[];
  receivers?: Receiver[];
  fanoutKind?: string;
}

interface Frame {
  id: string;
  title?: string; // display-friendly name, e.g. "English.greet"
  file: string;
  language: string;
  startLine: number;
  endLine: number;
  source: string;
  calls: CallSite[];
}

interface SearchResult {
  targetId: string;
  label: string;
  file: string;
  line: number;
}

interface TypeInfoOut {
  kind: string;
  name: string;
  type: string;
  definedAt?: string;
  doc?: string;
  targetId?: string;
  definition?: string; // expanded type shape (members), multi-line
}

interface Usage {
  callId?: string;
  choice?: number;
  caller: string;
  callerTitle: string;
  file: string;
  line: number;
  kind: "call" | "interface" | "ref";
  excerpt: string;
  excerptLine: number;
}

// A registered call target: a function-like declaration we can show a body
// for. `decl` is the node whose text becomes the frame source.
interface FuncInfo {
  id: string;
  name: string; // human label, e.g. "English.greet" or "runGreeter"
  decl: TNode;
  frame?: Frame; // cached
}

interface CallInfo {
  kind: CallKind;
  target?: string;
  candidates?: { targetId: string; label: string }[];
  receivers?: Receiver[];
  fanoutKind?: string;
}

// A synthetic frame for an Angular component template.
interface TemplateInfo {
  id: string;
  name: string; // e.g. "AppComponent ⟨template⟩"
  file: string;
  line: number;
  frame: Frame;
}

// One method invocation found in a template binding expression.
interface TplCall {
  name: string; // method name, e.g. "onClick"
  displayName: string; // "onClick" or "user.fullName"
  start: number; // UTF-16 offset into the template HTML
  end: number;
  implicit: boolean; // receiver is the component instance (this / implicit)
}

// ---- engine ----

class TSEngine {
  private project!: Project;
  private funcs = new Map<string, FuncInfo>();
  private callsById = new Map<string, CallInfo>();
  // Keyed by the interface/abstract-class declaration's node key; value is
  // the concrete classes that implement/extend it.
  private implementers = new Map<string, ClassDeclaration[]>();
  private templates = new Map<string, TemplateInfo>();
  private loaded = false;

  load(dir: string): { funcs: number } {
    const tsconfig = `${dir}/tsconfig.json`;
    if (fileExists(tsconfig)) {
      this.project = new Project({ tsConfigFilePath: tsconfig });
    } else {
      this.project = new Project({
        compilerOptions: { allowJs: true, skipLibCheck: true },
      });
      this.project.addSourceFilesAtPaths([
        `${dir}/**/*.ts`,
        `${dir}/**/*.tsx`,
        `!${dir}/**/node_modules/**`,
      ]);
    }

    this.registerTargets();
    this.buildImplementers();
    // Function frames are built lazily on first frame() request (and cached),
    // which also bounds fan-out reference searches to frames the user views.
    this.indexTemplates();
    this.loaded = true;
    return { funcs: this.funcs.size };
  }

  // Index every @Component's template (inline or templateUrl) as a synthetic
  // frame whose call sites resolve to the component's own methods. Component
  // methods are already registered as targets, so template calls reuse them.
  private indexTemplates() {
    for (const sf of this.project.getSourceFiles()) {
      if (sf.isInNodeModules() || sf.isDeclarationFile()) continue;
      for (const cls of sf.getClasses()) {
        const dec = cls.getDecorator("Component");
        if (!dec) continue;
        const obj = dec.getArguments()[0];
        if (!obj || !Node.isObjectLiteralExpression(obj)) continue;

        let html: string | undefined;
        let templateFile = sf.getFilePath();
        let templateId = "";
        let startLine = 1;

        const tmplProp = obj.getProperty("template");
        const urlProp = obj.getProperty("templateUrl");
        if (tmplProp && Node.isPropertyAssignment(tmplProp)) {
          const init = tmplProp.getInitializer();
          if (init && (Node.isStringLiteral(init) || Node.isNoSubstitutionTemplateLiteral(init))) {
            html = init.getLiteralText();
            startLine = init.getStartLineNumber();
            templateId = `${sf.getFilePath()}#template@${init.getStart()}`;
          }
        } else if (urlProp && Node.isPropertyAssignment(urlProp)) {
          const init = urlProp.getInitializer();
          if (init && (Node.isStringLiteral(init) || Node.isNoSubstitutionTemplateLiteral(init))) {
            const abs = resolvePath(dirname(sf.getFilePath()), init.getLiteralText());
            try {
              html = readFileSync(abs, "utf8");
              templateFile = abs;
              templateId = `${abs}#template`;
            } catch {
              html = undefined;
            }
          }
        }
        if (html === undefined || !templateId) continue;

        const className = cls.getName() ?? "(anonymous)";
        const calls: CallSite[] = [];
        for (const tc of collectTemplateCalls(html, templateFile)) {
          const callId = `${templateId}:${tc.start}`;
          let kind: CallKind = "indirect";
          let target: string | undefined;
          if (tc.implicit) {
            const m = cls.getMethod(tc.name);
            if (m && m.getBody()) {
              const tid = targetId(m);
              if (this.funcs.has(tid)) {
                kind = "direct";
                target = tid;
              }
            }
          }
          this.callsById.set(callId, { kind, target });
          calls.push({
            id: callId,
            spanStart: tc.start,
            spanEnd: tc.end,
            displayName: tc.displayName,
            kind,
            targetId: target,
          });
        }

        const frame: Frame = {
          id: templateId,
          title: `${className} ⟨template⟩`,
          file: templateFile,
          language: "html",
          startLine,
          endLine: startLine + html.split("\n").length - 1,
          source: html,
          calls,
        };
        this.templates.set(templateId, {
          id: templateId,
          name: `${className} ⟨template⟩`,
          file: templateFile,
          line: startLine,
          frame,
        });
      }
    }
  }

  // Map each interface (and abstract class) to the concrete classes that
  // implement (or extend) it, so an interface-dispatched call can offer its
  // candidate bodies. This mirrors the Go engine's implementer index.
  // Coverage is the explicit `implements`/`extends` graph; purely
  // structural (duck-typed) implementers without a clause are not detected.
  private buildImplementers() {
    for (const sf of this.project.getSourceFiles()) {
      if (sf.isInNodeModules() || sf.isDeclarationFile()) continue;
      for (const cls of sf.getClasses()) {
        for (const impl of cls.getImplements()) {
          const owner = heritageDecl(impl);
          if (owner) this.addImplementer(owner, cls);
        }
        const ext = cls.getExtends();
        if (ext) {
          const owner = heritageDecl(ext);
          if (owner && Node.isClassDeclaration(owner)) this.addImplementer(owner, cls);
        }
      }
    }
  }

  private addImplementer(ownerDecl: TNode, cls: ClassDeclaration) {
    const key = targetId(ownerDecl);
    const arr = this.implementers.get(key) ?? [];
    arr.push(cls);
    this.implementers.set(key, arr);
  }

  // Pass 1: register every function-like declaration as a target.
  private registerTargets() {
    for (const sf of this.project.getSourceFiles()) {
      if (sf.isInNodeModules() || sf.isDeclarationFile()) continue;

      for (const fn of sf.getFunctions()) {
        const name = fn.getName();
        if (name) this.register(fn, name);
      }
      for (const cls of sf.getClasses()) {
        const cname = cls.getName() ?? "(anonymous)";
        for (const m of cls.getMethods()) {
          if (m.getBody()) this.register(m, `${cname}.${m.getName()}`);
        }
      }
      for (const vd of sf.getVariableDeclarations()) {
        const init = vd.getInitializer();
        if (init && (Node.isArrowFunction(init) || Node.isFunctionExpression(init))) {
          this.register(vd, vd.getName());
        }
      }
    }
  }

  private register(decl: TNode, name: string) {
    const id = targetId(decl);
    if (!this.funcs.has(id)) this.funcs.set(id, { id, name, decl });
  }

  // Pass 2: walk each target's body, resolve call sites, populate
  // callsById. Frames are built (and cached) here too.

  private buildFrame(fi: FuncInfo): Frame {
    const sf = fi.decl.getSourceFile();
    const full = sf.getFullText();
    const base = fi.decl.getStart();
    const end = fi.decl.getEnd();
    const source = full.slice(base, end);

    const calls: CallSite[] = [];
    const body = bodyNode(fi.decl);
    if (body) {
      for (const call of body.getDescendantsOfKind(SyntaxKind.CallExpression)) {
        const cs = this.resolveCall(call, base);
        if (cs) calls.push(cs);
      }
    }

    return {
      id: fi.id,
      title: fi.name,
      file: sf.getFilePath(),
      language: sf.getFilePath().endsWith(".tsx") ? "tsx" : "typescript",
      startLine: fi.decl.getStartLineNumber(),
      endLine: fi.decl.getEndLineNumber(),
      source,
      calls,
    };
  }

  private resolveCall(call: CallExpression, base: number): CallSite | null {
    const expr = call.getExpression();
    const nameNode = callNameNode(expr);
    if (!nameNode) return null; // no name token to anchor on (IIFE, etc.)

    const id = `${call.getSourceFile().getFilePath()}:${call.getStart()}`;
    const info = this.classify(expr);
    this.callsById.set(id, info);

    return {
      id,
      spanStart: nameNode.getStart() - base,
      spanEnd: nameNode.getEnd() - base,
      displayName: displayName(expr),
      kind: info.kind,
      targetId: info.target,
      candidates: info.candidates,
      receivers: info.receivers,
      fanoutKind: info.fanoutKind,
    };
  }

  private classify(expr: TNode): CallInfo {
    const fan = this.fanoutFor(expr);
    if (fan) return fan;

    const nameNode = callNameNode(expr);
    let sym = nameNode?.getSymbol();
    if (sym) {
      const aliased = sym.getAliasedSymbol();
      if (aliased) sym = aliased;
    }
    const decls = sym?.getDeclarations() ?? [];
    const decl = pickDecl(decls);
    if (!decl) return { kind: "indirect" };

    // Anything declared in a lib/.d.ts or under node_modules is external:
    // render it as a non-expandable "ext" call (like Go's stdlib calls),
    // even when its declaration happens to be an interface method (e.g.
    // console.log resolves to the Console interface in lib.dom.d.ts).
    const dsf = decl.getSourceFile();
    if (dsf.isDeclarationFile() || dsf.isInNodeModules()) {
      return { kind: "direct" };
    }

    // In-project interface / abstract method dispatch — no single body.
    // Candidates (the concrete impls) are enumerated in Phase 5c.
    if (
      Node.isMethodSignature(decl) ||
      (Node.isMethodDeclaration(decl) && decl.isAbstract())
    ) {
      return { kind: "interface", candidates: this.candidatesFor(decl) };
    }

    const norm = normalizeDecl(decl);
    const tid = targetId(norm);
    if (this.funcs.has(tid)) return { kind: "direct", target: tid };

    // A function/method we don't have a registered body for.
    if (isFunctionLike(norm)) return { kind: "direct" };

    return { kind: "indirect" };
  }

  // Fan-out detection: a `subject.next(v)` / `emitter.emit(v)` on an RxJS
  // Subject-family or Angular EventEmitter reaches every `.subscribe()` site.
  // Returns null for any non-fan-out call (the cheap gate is the type check).
  private fanoutFor(expr: TNode): CallInfo | null {
    if (!Node.isPropertyAccessExpression(expr)) return null;
    const method = expr.getName();
    if (method !== "next" && method !== "emit") return null;
    const receiver = expr.getExpression();
    let sym;
    try {
      sym = receiver.getType().getSymbol();
    } catch {
      return null;
    }
    const typeName = sym?.getName() ?? "";
    if (!/^(Subject|BehaviorSubject|ReplaySubject|AsyncSubject|EventEmitter)$/.test(typeName)) {
      return null;
    }
    // Only the real RxJS / Angular types — not a user class named "Subject".
    // Require rxjs/@angular-core to be a path *segment* (a directory) of the
    // declaring file, matching how both packages actually resolve
    // (node_modules/rxjs/…, node_modules/@angular/core/…). A trailing "."
    // is intentionally NOT accepted, so a user's own file literally named
    // rxjs.ts — or a dir like src/rxjs-helpers/ — doesn't false-positive.
    const origin = sym?.getDeclarations()?.[0]?.getSourceFile().getFilePath() ?? "";
    if (!/(^|[/\\])(rxjs|@angular[/\\]core)([/\\]|$)/.test(origin)) return null;

    return { kind: "fanout", fanoutKind: "subscribers", receivers: this.resolveSubscribers(receiver) };
  }

  // Find every `.subscribe(cb)` on the same observable symbol and turn each
  // callback into a receiver. Uses the language service's reference search.
  private resolveSubscribers(receiver: TNode): Receiver[] {
    const refSource = Node.isIdentifier(receiver)
      ? receiver
      : Node.isPropertyAccessExpression(receiver)
        ? receiver.getNameNode()
        : undefined;
    if (!refSource) return [];
    let refs: TNode[] = [];
    try {
      refs = (refSource as unknown as { findReferencesAsNodes: () => TNode[] }).findReferencesAsNodes();
    } catch {
      return [];
    }

    const out: Receiver[] = [];
    const seen = new Set<string>();
    for (const ref of refs) {
      let access = ref.getParent();
      // `this.events.subscribe(...)`: the ref is the `.name` of the inner
      // `this.events` access, so `.subscribe` is one level up. Climb past the
      // access whose name *is* this ref. (A module-level `events.subscribe`
      // ref is the access's `.expression`, not its `.name`, so we don't climb.)
      if (
        access &&
        Node.isPropertyAccessExpression(access) &&
        access.getName() !== "subscribe" &&
        access.getNameNode() === ref
      ) {
        access = access.getParent();
      }
      if (!access || !Node.isPropertyAccessExpression(access) || access.getName() !== "subscribe") continue;
      const call = access.getParent();
      if (!call || !Node.isCallExpression(call)) continue;
      const arg = call.getArguments()[0];
      if (!arg) continue;
      const cb = this.callbackNode(arg);
      if (!cb) continue;
      const tid = this.registerCallback(cb);
      if (seen.has(tid)) continue;
      seen.add(tid);
      out.push({
        targetId: tid,
        label: this.subscriberLabel(call),
        provenance: `subscribe at ${call.getSourceFile().getBaseName()}:${call.getStartLineNumber()}`,
        confidence: "high",
      });
    }
    out.sort((a, b) => (a.label < b.label ? -1 : a.label > b.label ? 1 : 0));
    return out;
  }

  // The function body a subscribe argument runs: an inline arrow/function, a
  // named function reference, or the `next` member of an observer object.
  private callbackNode(arg: TNode): TNode | undefined {
    if (Node.isArrowFunction(arg) || Node.isFunctionExpression(arg)) return arg;
    if (Node.isIdentifier(arg)) {
      const d = arg.getSymbol()?.getDeclarations()?.[0];
      if (d && isFunctionLike(normalizeDecl(d))) return normalizeDecl(d);
    }
    if (Node.isObjectLiteralExpression(arg)) {
      const next = arg.getProperty("next");
      if (next && Node.isPropertyAssignment(next)) {
        const v = next.getInitializer();
        if (v && (Node.isArrowFunction(v) || Node.isFunctionExpression(v))) return v;
      }
    }
    return undefined;
  }

  // Register a subscribe callback (often an inline arrow) as a target so it can
  // be framed like any other function.
  private registerCallback(node: TNode): string {
    const id = targetId(node);
    if (!this.funcs.has(id)) this.funcs.set(id, { id, name: "subscriber", decl: node });
    return id;
  }

  private subscriberLabel(call: TNode): string {
    let node: TNode | undefined = call.getParent();
    while (node) {
      const named = node as { getName?: () => string | undefined };
      if ((Node.isMethodDeclaration(node) || Node.isFunctionDeclaration(node)) && named.getName?.()) {
        return `${named.getName()}()`;
      }
      if (Node.isClassDeclaration(node) && named.getName?.()) return named.getName()!;
      node = node.getParent();
    }
    return `${call.getSourceFile().getBaseName()}:${call.getStartLineNumber()}`;
  }

  // Enumerate the concrete-method candidates for an interface/abstract
  // method declaration: every implementing class's method of the same name
  // that we have a registered body for. Stable (sorted) so choice indexes
  // are deterministic.
  private candidatesFor(methodDecl: TNode): { targetId: string; label: string }[] {
    const owner = methodDecl.getParent();
    if (!owner) return [];
    const name = (methodDecl as { getName?: () => string }).getName?.() ?? "";
    if (!name) return [];

    const out: { targetId: string; label: string }[] = [];
    for (const cls of this.implementers.get(targetId(owner)) ?? []) {
      const m = cls.getMethod(name);
      if (!m || !m.getBody()) continue;
      const id = targetId(m);
      if (this.funcs.has(id)) {
        out.push({ targetId: id, label: `${cls.getName() ?? "?"}.${name}` });
      }
    }
    out.sort((a, b) => (a.label < b.label ? -1 : a.label > b.label ? 1 : 0));
    return out;
  }

  lookupSymbol(name: string): string {
    if (!this.loaded) throw new Error("project not loaded");
    if (name.startsWith("file:")) return name; // file pseudo-target
    if (this.funcs.has(name) || this.templates.has(name)) return name; // exact id round-trip

    const q = name.toLowerCase();
    const matches: { id: string; label: string }[] = [];
    for (const fi of this.funcs.values()) {
      const base = fi.name.includes(".") ? fi.name.slice(fi.name.lastIndexOf(".") + 1) : fi.name;
      if (fi.name === name || base === name) matches.push({ id: fi.id, label: fi.name });
    }
    for (const t of this.templates.values()) {
      if (t.name === name || t.name.toLowerCase().includes(q)) matches.push({ id: t.id, label: t.name });
    }
    if (matches.length === 0) throw new Error(`no symbol matches ${JSON.stringify(name)}`);
    matches.sort((a, b) => (a.label < b.label ? -1 : a.label > b.label ? 1 : 0));
    return matches[0].id;
  }

  frame(id: string): Frame {
    if (!this.loaded) throw new Error("project not loaded");
    if (id.startsWith("file:")) return this.fileFrame(id.slice(5));
    const fi = this.funcs.get(id);
    if (fi) {
      if (!fi.frame) fi.frame = this.buildFrame(fi);
      return fi.frame;
    }
    const tpl = this.templates.get(id);
    if (tpl) return tpl.frame;
    throw new Error(`unknown target ${JSON.stringify(id)}`);
  }

  // Distinct source files holding at least one registered function.
  files(): string[] {
    if (!this.loaded) return [];
    const set = new Set<string>();
    for (const fi of this.funcs.values()) set.add(fi.decl.getSourceFile().getFilePath());
    return [...set].sort();
  }

  // A whole-file Frame: full source plus every call site in the file (offsets
  // file-relative). Call IDs match those built during indexing, so expanding
  // a call from the file view goes through the normal frameForCall path.
  private fileFrame(path: string): Frame {
    const sf = this.project.getSourceFile(path);
    if (!sf) throw new Error(`unknown file ${JSON.stringify(path)}`);
    const source = sf.getFullText();

    const calls: CallSite[] = [];
    const seen = new Set<string>();
    // Snapshot the funcs: classify() below resolves fan-out receivers, which
    // registerCallback()s new subscriber functions into this.funcs — a live
    // Map iterator would then walk those too. The `seen` set is the real
    // guard against duplicates: a subscribe callback is itself a registered
    // func nested inside its enclosing function, so its inner calls would
    // otherwise be emitted twice (once per containing function). Call ids are
    // position-based, so the same physical call dedupes cleanly.
    for (const fi of [...this.funcs.values()]) {
      if (fi.decl.getSourceFile().getFilePath() !== path) continue;
      const body = bodyNode(fi.decl);
      if (!body) continue;
      for (const call of body.getDescendantsOfKind(SyntaxKind.CallExpression)) {
        const expr = call.getExpression();
        const nameNode = callNameNode(expr);
        if (!nameNode) continue;
        const id = `${path}:${call.getStart()}`;
        if (seen.has(id)) continue;
        seen.add(id);
        // Persist so a call expanded straight from the file view resolves
        // through frameForCall, which looks up callsById. Without this, a call
        // whose enclosing function frame was never built (the file view shows
        // every function's calls without building their frames) would throw
        // "unknown call" on expand. resolveCall caches the same way.
        const info = this.callsById.get(id) ?? this.classify(expr);
        this.callsById.set(id, info);
        calls.push({
          id,
          spanStart: nameNode.getStart(), // file-relative (base 0)
          spanEnd: nameNode.getEnd(),
          displayName: displayName(expr),
          kind: info.kind,
          targetId: info.target,
          candidates: info.candidates,
          receivers: info.receivers,
          fanoutKind: info.fanoutKind,
        });
      }
    }
    calls.sort((a, b) => a.spanStart - b.spanStart);

    return {
      id: `file:${path}`,
      title: path.slice(path.lastIndexOf("/") + 1),
      file: path,
      language: path.endsWith(".tsx") ? "tsx" : "typescript",
      startLine: 1,
      endLine: source.split("\n").length,
      source,
      calls,
    };
  }

  frameForCall(callId: string, choice: number): Frame {
    if (!this.loaded) throw new Error("project not loaded");
    const info = this.callsById.get(callId);
    if (!info) throw new Error(`unknown call ${JSON.stringify(callId)}`);
    if (info.kind === "direct") {
      if (!info.target) throw new Error("call has no expandable target");
      return this.frame(info.target);
    }
    if (info.kind === "interface") {
      const cands = info.candidates ?? [];
      if (cands.length === 0) throw new Error("no candidate implementations found for interface call");
      const i = choice < 0 || choice >= cands.length ? 0 : choice;
      return this.frame(cands[i].targetId);
    }
    if (info.kind === "fanout") {
      const recs = info.receivers ?? [];
      if (recs.length === 0) throw new Error("no receivers for fan-out call");
      const i = choice < 0 || choice >= recs.length ? 0 : choice;
      return this.frame(recs[i].targetId);
    }
    throw new Error(`call is ${info.kind}; not expandable`);
  }

  // Resolve the identifier at a UTF-16 offset into the frame source. A
  // negative offset describes the target's own declaration (note refs).
  typeInfo(id: string, offset: number): TypeInfoOut | null {
    if (!this.loaded) return null;
    if (offset < 0) return this.describeTarget(id);
    let sf;
    let absPos: number;
    if (id.startsWith("file:")) {
      const found = this.project.getSourceFile(id.slice(5));
      if (!found) return null;
      sf = found;
      absPos = offset;
    } else {
      const fi = this.funcs.get(id);
      if (!fi) return null;
      sf = fi.decl.getSourceFile();
      absPos = fi.decl.getStart() + offset;
    }
    const node = sf.getDescendantAtPos(absPos);
    if (!node || !Node.isIdentifier(node)) return null;

    let type = "";
    try {
      const t = node.getType();
      const sigs = t.getCallSignatures();
      if (sigs.length > 0) {
        // A function/method — render its signature, not "typeof fn".
        const sig = sigs[0];
        const params = sig
          .getParameters()
          .map((p) => p.getDeclarations()[0]?.getText() ?? p.getName())
          .join(", ");
        type = `(${params}) => ${sig.getReturnType().getText(node)}`;
      } else {
        type = t.getText(node);
      }
    } catch {
      type = "";
    }
    let sym = node.getSymbol();
    if (sym) {
      const aliased = sym.getAliasedSymbol();
      if (aliased) sym = aliased;
    }
    const decl = sym?.getDeclarations()?.[0];

    const out: TypeInfoOut = { kind: "symbol", name: node.getText(), type };
    try {
      const def = this.typeDefinition(node.getType());
      if (def) out.definition = def;
    } catch {
      /* definition is best-effort */
    }
    if (decl) {
      out.definedAt = `${decl.getSourceFile().getFilePath()}:${decl.getStartLineNumber()}`;
      out.kind = declKind(decl);
      const tid = targetId(normalizeDecl(decl));
      if (this.funcs.has(tid)) out.targetId = tid;
      const jsdocs = (decl as { getJsDocs?: () => { getDescription(): string }[] }).getJsDocs?.();
      const d = jsdocs && jsdocs[0]?.getDescription()?.trim();
      if (d) out.doc = d;
    }
    return out;
  }

  // The places a target is referenced inside registered function bodies,
  // via the language service's reference search. A reference whose parent
  // is a call it names becomes kind "call" (or "interface" when dispatch
  // goes through an interface/abstract method); anything else is a value
  // reference. References inside Angular template HTML are not covered
  // (templates aren't TS AST nodes).
  usages(id: string): Usage[] {
    if (!this.loaded) throw new Error("project not loaded");
    const fi = this.funcs.get(id);
    if (!fi) throw new Error(`unknown target ${JSON.stringify(id)}`);

    // Anchor the search on the declaration's name node; anonymous targets
    // (inline subscribe callbacks) have no name to find references for.
    const nameNode = (fi.decl as { getNameNode?: () => TNode | undefined }).getNameNode?.();
    if (!nameNode) return [];
    let refs: TNode[] = [];
    try {
      refs = (nameNode as unknown as { findReferencesAsNodes: () => TNode[] }).findReferencesAsNodes();
    } catch {
      return [];
    }

    const out: Usage[] = [];
    const seen = new Set<string>();
    for (const ref of refs) {
      const sf = ref.getSourceFile();
      if (sf.isInNodeModules() || sf.isDeclarationFile()) continue;
      // Skip declaration name nodes: the target's own declaration, sibling
      // implementations of the same interface member, and the interface's
      // method signature (the language service groups them all as
      // references). A PropertyAccessExpression's name node is a real
      // usage, so only declaration forms are filtered.
      const refParent = ref.getParent();
      if (
        refParent &&
        (Node.isMethodDeclaration(refParent) ||
          Node.isMethodSignature(refParent) ||
          Node.isFunctionDeclaration(refParent) ||
          Node.isFunctionExpression(refParent) ||
          Node.isVariableDeclaration(refParent) ||
          Node.isPropertyDeclaration(refParent) ||
          Node.isPropertySignature(refParent)) &&
        (refParent as { getNameNode?: () => TNode | undefined }).getNameNode?.() === ref
      ) {
        continue;
      }
      const caller = this.enclosingFunc(ref);
      if (!caller) continue; // reference outside any indexed function body
      const dedupe = `${sf.getFilePath()}:${ref.getStart()}`;
      if (seen.has(dedupe)) continue;
      seen.add(dedupe);

      const usage: Usage = {
        caller: caller.id,
        callerTitle: caller.name,
        file: sf.getFilePath(),
        line: ref.getStartLineNumber(),
        kind: "ref",
        excerpt: "",
        excerptLine: 0,
      };

      // Is this reference the name of a call? `foo()` → ref is the callee
      // ident; `x.foo()` → ref is the property-access name node.
      const call = this.callFor(ref);
      if (call) {
        const expr = call.getExpression();
        const callId = `${sf.getFilePath()}:${call.getStart()}`;
        // Persist the classification so expanding this usage's call site
        // resolves through frameForCall (same pattern as fileFrame).
        const info = this.callsById.get(callId) ?? this.classify(expr);
        this.callsById.set(callId, info);
        if (info.kind === "direct" && info.target === id) {
          usage.kind = "call";
          usage.callId = callId;
          usage.choice = 0;
        } else if (info.kind === "interface") {
          const idx = (info.candidates ?? []).findIndex((c) => c.targetId === id);
          if (idx >= 0) {
            usage.kind = "interface";
            usage.callId = callId;
            usage.choice = idx;
          }
        }
      }

      [usage.excerpt, usage.excerptLine] = excerptAround(
        sf.getFullText(),
        usage.line,
        caller.decl.getStartLineNumber(),
        caller.decl.getEndLineNumber(),
      );
      out.push(usage);
    }

    out.sort((a, b) =>
      a.file !== b.file ? (a.file < b.file ? -1 : 1) : a.line - b.line,
    );
    return out;
  }

  // The innermost registered function whose declaration encloses node.
  private enclosingFunc(node: TNode): FuncInfo | undefined {
    let cur: TNode | undefined = node.getParent();
    while (cur) {
      const fi = this.funcs.get(targetId(cur));
      if (fi) return fi;
      cur = cur.getParent();
    }
    return undefined;
  }

  // The CallExpression that `ref` names, or undefined when the reference
  // isn't a call's name token.
  private callFor(ref: TNode): CallExpression | undefined {
    let expr: TNode | undefined = ref;
    const parent = ref.getParent();
    if (parent && Node.isPropertyAccessExpression(parent) && parent.getNameNode() === ref) {
      expr = parent;
    }
    const call = expr?.getParent();
    if (call && Node.isCallExpression(call) && call.getExpression() === expr) {
      return call;
    }
    return undefined;
  }

  // The TypeInfo of a target's own declaration: signature, location, doc.
  private describeTarget(id: string): TypeInfoOut | null {
    const fi = this.funcs.get(id);
    if (!fi) return null;
    const decl = fi.decl;
    let type = "";
    try {
      const sigs = decl.getType().getCallSignatures();
      if (sigs.length > 0) {
        const sig = sigs[0];
        const params = sig
          .getParameters()
          .map((p) => p.getDeclarations()[0]?.getText() ?? p.getName())
          .join(", ");
        type = `(${params}) => ${sig.getReturnType().getText(decl)}`;
      } else {
        type = decl.getType().getText(decl);
      }
    } catch {
      type = "";
    }
    const out: TypeInfoOut = {
      kind: "func",
      name: fi.name,
      type,
      definedAt: `${decl.getSourceFile().getFilePath()}:${decl.getStartLineNumber()}`,
      targetId: id,
    };
    const jsdocs = (decl as { getJsDocs?: () => { getDescription(): string }[] }).getJsDocs?.();
    const d = jsdocs && jsdocs[0]?.getDescription()?.trim();
    if (d) out.doc = d;
    return out;
  }

  // The expanded shape of a type whose name alone isn't telling: one line
  // per member, from each member's declaration text. Returns "" for types
  // the `type` string already describes (primitives, callables, arrays,
  // unions) and for very large types.
  private typeDefinition(t: TsType): string {
    if (t.getCallSignatures().length > 0) return "";
    if (
      t.isString() ||
      t.isNumber() ||
      t.isBoolean() ||
      t.isLiteral() ||
      t.isEnum() ||
      t.isUnion() ||
      t.isArray() ||
      t.isAny() ||
      t.isUnknown()
    ) {
      return "";
    }
    const props = t.getProperties();
    if (props.length === 0 || props.length > 24) return "";
    const lines: string[] = [];
    for (const p of props) {
      const d = p.getDeclarations()[0];
      let text = (d?.getText() ?? p.getName()).split("\n")[0].trim();
      if (text.length > 100) text = text.slice(0, 97) + "…";
      lines.push("    " + text);
    }
    return "{\n" + lines.join("\n") + "\n}";
  }

  search(query: string, limit: number): SearchResult[] {
    if (!this.loaded) return [];
    const q = query.toLowerCase();
    const out: SearchResult[] = [];
    const ids = [...this.funcs.keys()].sort();
    for (const id of ids) {
      const fi = this.funcs.get(id)!;
      if (q && !fi.name.toLowerCase().includes(q)) continue;
      const sf = fi.decl.getSourceFile();
      out.push({
        targetId: id,
        label: fi.name,
        file: sf.getFilePath(),
        line: fi.decl.getStartLineNumber(),
      });
      if (out.length >= limit) break;
    }
    // Surface component templates too.
    for (const t of [...this.templates.values()].sort((a, b) => (a.id < b.id ? -1 : 1))) {
      if (out.length >= limit) break;
      if (q && !t.name.toLowerCase().includes(q)) continue;
      out.push({ targetId: t.id, label: t.name, file: t.file, line: t.line });
    }
    return out;
  }
}

// Parse an Angular template and collect every method invocation in a binding
// expression (event handler, interpolation, property binding). Offsets are
// UTF-16 string indices into `html`, matching how the frontend reads them.
function collectTemplateCalls(html: string, fileForDiag: string): TplCall[] {
  let parsed;
  try {
    parsed = parseTemplate(html, fileForDiag, { preserveWhitespaces: true });
  } catch {
    return [];
  }

  const exprs: AST[] = [];
  class NodeCollector extends TmplAstRecursiveVisitor {
    visitBoundEvent(e: { handler?: AST }) {
      if (e.handler) exprs.push(e.handler);
    }
    visitBoundText(t: { value?: AST }) {
      if (t.value) exprs.push(t.value);
    }
    visitBoundAttribute(a: { value?: AST }) {
      if (a.value) exprs.push(a.value);
    }
  }
  try {
    tmplAstVisitAll(new NodeCollector(), parsed.nodes);
  } catch {
    return [];
  }

  const calls: TplCall[] = [];
  class CallFinder extends RecursiveAstVisitor {
    visitCall(node: { receiver?: unknown }, ctx: unknown) {
      const recv = node.receiver as
        | { name?: string; nameSpan?: { start: number; end: number }; receiver?: unknown }
        | undefined;
      if (recv && recv.nameSpan && typeof recv.name === "string") {
        const implicit = recv.receiver instanceof ImplicitReceiver; // covers `this.x` and `x`
        const inner = recv.receiver as { name?: string } | undefined;
        const displayName =
          implicit || !inner || typeof inner.name !== "string"
            ? recv.name
            : `${inner.name}.${recv.name}`;
        calls.push({ name: recv.name, displayName, start: recv.nameSpan.start, end: recv.nameSpan.end, implicit });
      }
      super.visitCall(node as never, ctx);
    }
  }
  for (const e of exprs) {
    const ast = (e as { ast?: AST }).ast ?? e;
    try {
      (ast as { visit: (v: RecursiveAstVisitor, ctx: unknown) => void }).visit(new CallFinder(), null);
    } catch {
      /* skip a malformed expression */
    }
  }
  return calls;
}

// ---- helpers ----

function targetId(node: TNode): string {
  return `${node.getSourceFile().getFilePath()}#${node.getStart()}`;
}

// Up to two lines of context either side of line (1-based), clamped to
// [bodyStart, bodyEnd]. Returns the excerpt and its 1-based first line.
function excerptAround(
  fullText: string,
  line: number,
  bodyStart: number,
  bodyEnd: number,
): [string, number] {
  const lines = fullText.split("\n");
  const start = Math.max(line - 2, Math.max(bodyStart, 1));
  const end = Math.min(line + 2, Math.min(bodyEnd, lines.length));
  if (start > end) return ["", 0];
  return [lines.slice(start - 1, end).join("\n"), start];
}

// Resolve a heritage clause (`implements X` / `extends X`) to the interface
// or class declaration it names, following import aliases.
function heritageDecl(node: TNode): TNode | undefined {
  const expr = (node as { getExpression?: () => TNode }).getExpression?.();
  if (!expr) return undefined;
  let sym = expr.getSymbol();
  if (sym) {
    const aliased = sym.getAliasedSymbol();
    if (aliased) sym = aliased;
  }
  const decls = sym?.getDeclarations() ?? [];
  return decls.find((d) => Node.isInterfaceDeclaration(d) || Node.isClassDeclaration(d));
}

// The function-name node a call decorates: `foo` in foo(), `bar` in x.bar().
function callNameNode(expr: TNode): TNode | undefined {
  if (Node.isIdentifier(expr)) return expr;
  if (Node.isPropertyAccessExpression(expr)) return expr.getNameNode();
  return undefined;
}

function displayName(expr: TNode): string {
  if (Node.isPropertyAccessExpression(expr)) {
    const obj = expr.getExpression();
    if (Node.isIdentifier(obj)) return `${obj.getText()}.${expr.getName()}`;
    return expr.getName();
  }
  if (Node.isIdentifier(expr)) return expr.getText();
  return expr.getText();
}

// A short kind label for a declaration, for the type-info card.
function declKind(decl: TNode): string {
  if (
    Node.isFunctionDeclaration(decl) ||
    Node.isMethodDeclaration(decl) ||
    Node.isMethodSignature(decl) ||
    Node.isArrowFunction(decl) ||
    Node.isFunctionExpression(decl) ||
    Node.isConstructorDeclaration(decl)
  ) {
    return "func";
  }
  if (
    Node.isClassDeclaration(decl) ||
    Node.isInterfaceDeclaration(decl) ||
    Node.isTypeAliasDeclaration(decl) ||
    Node.isEnumDeclaration(decl)
  ) {
    return "type";
  }
  if (Node.isPropertyDeclaration(decl) || Node.isPropertySignature(decl) || Node.isPropertyAssignment(decl)) {
    return "field";
  }
  if (Node.isParameterDeclaration(decl)) return "param";
  if (Node.isVariableDeclaration(decl)) return "var";
  if (Node.isEnumMember(decl)) return "const";
  return "symbol";
}

// Prefer a declaration that has a body (the impl among overload signatures).
function pickDecl(decls: TNode[]): TNode | undefined {
  for (const d of decls) {
    const b = (d as { getBody?: () => unknown }).getBody?.();
    if (b) return d;
  }
  return decls[0];
}

// Map an arrow/function-expression initializer back to its VariableDeclaration
// so its id matches what we registered.
function normalizeDecl(decl: TNode): TNode {
  if (Node.isArrowFunction(decl) || Node.isFunctionExpression(decl)) {
    const parent = decl.getParent();
    if (parent && Node.isVariableDeclaration(parent)) return parent;
  }
  return decl;
}

function isFunctionLike(node: TNode): boolean {
  return (
    Node.isFunctionDeclaration(node) ||
    Node.isMethodDeclaration(node) ||
    Node.isArrowFunction(node) ||
    Node.isFunctionExpression(node) ||
    Node.isConstructorDeclaration(node) ||
    (Node.isVariableDeclaration(node) && hasFuncInitializer(node))
  );
}

function hasFuncInitializer(vd: TNode): boolean {
  if (!Node.isVariableDeclaration(vd)) return false;
  const init = vd.getInitializer();
  return !!init && (Node.isArrowFunction(init) || Node.isFunctionExpression(init));
}

// The node to walk for call sites and whose text we show.
function bodyNode(decl: TNode): TNode | undefined {
  if (Node.isVariableDeclaration(decl)) {
    const init = decl.getInitializer();
    if (init && (Node.isArrowFunction(init) || Node.isFunctionExpression(init))) {
      return (init.getBody?.() as TNode | undefined) ?? init;
    }
    return undefined;
  }
  return (decl as { getBody?: () => TNode | undefined }).getBody?.();
}

function fileExists(p: string): boolean {
  try {
    // Bun/Node: statSync via fs
    return require("node:fs").existsSync(p);
  } catch {
    return false;
  }
}

// ---- JSON-RPC stdio loop ----

const engine = new TSEngine();

function handle(method: string, params: Record<string, unknown>): unknown {
  switch (method) {
    case "load":
      return engine.load(String(params.dir ?? "."));
    case "lookupSymbol":
      return { targetId: engine.lookupSymbol(String(params.name ?? "")) };
    case "frame":
      return engine.frame(String(params.targetId ?? ""));
    case "frameForCall":
      return engine.frameForCall(String(params.callId ?? ""), Number(params.choice ?? 0));
    case "search":
      return { results: engine.search(String(params.query ?? ""), Number(params.limit ?? 50)) };
    case "files":
      return { files: engine.files() };
    case "typeinfo":
      return { typeInfo: engine.typeInfo(String(params.targetId ?? ""), Number(params.offset ?? 0)) };
    case "usages":
      return { usages: engine.usages(String(params.targetId ?? "")) };
    default:
      throw new Error(`unknown method ${JSON.stringify(method)}`);
  }
}

function main() {
  const rl = createInterface({ input: process.stdin });
  rl.on("line", (line: string) => {
    const trimmed = line.trim();
    if (!trimmed) return;
    let id: unknown = null;
    try {
      const req = JSON.parse(trimmed) as { id?: unknown; method?: string; params?: Record<string, unknown> };
      id = req.id ?? null;
      const result = handle(String(req.method), req.params ?? {});
      process.stdout.write(JSON.stringify({ id, result }) + "\n");
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      process.stdout.write(JSON.stringify({ id, error: message }) + "\n");
    }
  });
  rl.on("close", () => process.exit(0));
}

main();
