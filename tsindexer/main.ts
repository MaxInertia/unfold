// unfold-tsindexer: the TypeScript indexing engine, run as a sidecar by
// the Go process. It speaks newline-delimited JSON-RPC over stdio and
// emits the same Frame JSON shape the Go engine produces (see
// internal/model). One request per line in on stdin, one response per line
// out on stdout. All logging goes to stderr so it never corrupts the
// protocol.
//
// Methods: load, lookupSymbol, frame, frameForCall, search.
//
// Phase 5b implements direct-call resolution; interface dispatch is
// classified (kind="interface") but candidates are filled in Phase 5c.

import { createInterface } from "node:readline";
import {
  Node,
  Project,
  SyntaxKind,
  type CallExpression,
  type ClassDeclaration,
  type Node as TNode,
} from "ts-morph";

// ---- wire types (mirror internal/model) ----

type CallKind = "direct" | "interface" | "indirect";

interface CallSite {
  id: string;
  spanStart: number;
  spanEnd: number;
  displayName: string;
  kind: CallKind;
  targetId?: string;
  candidates?: { targetId: string; label: string }[];
}

interface Frame {
  id: string;
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
}

// ---- engine ----

class TSEngine {
  private project!: Project;
  private funcs = new Map<string, FuncInfo>();
  private callsById = new Map<string, CallInfo>();
  // Keyed by the interface/abstract-class declaration's node key; value is
  // the concrete classes that implement/extend it.
  private implementers = new Map<string, ClassDeclaration[]>();
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
    this.indexCalls();
    this.loaded = true;
    return { funcs: this.funcs.size };
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
  private indexCalls() {
    for (const fi of this.funcs.values()) {
      fi.frame = this.buildFrame(fi);
    }
  }

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
    };
  }

  private classify(expr: TNode): CallInfo {
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
    if (this.funcs.has(name)) return name; // exact target id (picker round-trip)

    const matches: FuncInfo[] = [];
    for (const fi of this.funcs.values()) {
      const base = fi.name.includes(".") ? fi.name.slice(fi.name.lastIndexOf(".") + 1) : fi.name;
      if (fi.name === name || base === name) matches.push(fi);
    }
    if (matches.length === 0) throw new Error(`no symbol matches ${JSON.stringify(name)}`);
    matches.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
    return matches[0].id;
  }

  frame(id: string): Frame {
    if (!this.loaded) throw new Error("project not loaded");
    const fi = this.funcs.get(id);
    if (!fi) throw new Error(`unknown target ${JSON.stringify(id)}`);
    if (!fi.frame) fi.frame = this.buildFrame(fi);
    return fi.frame;
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
    throw new Error(`call is ${info.kind}; not expandable`);
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
    return out;
  }
}

// ---- helpers ----

function targetId(node: TNode): string {
  return `${node.getSourceFile().getFilePath()}#${node.getStart()}`;
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
