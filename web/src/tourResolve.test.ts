import { describe, expect, test } from "bun:test";
import { buildTree, findCall, matchFile, parseExpand, resolveStop, symbolMatches } from "./tourResolve";
import type { CallSite, Frame } from "./types";

// The rules that turn what an author writes — a name, a path — into what the
// view opens. Each one is a way a tour written from memory, or by a model
// reading the code, can still land on the right place.

function call(id: string, displayName: string, targetId?: string): CallSite {
  return { id, spanStart: 0, spanEnd: 0, displayName, kind: "direct", targetId };
}

function frame(id: string, calls: CallSite[]): Frame {
  return {
    id,
    file: `/p/${id}.go`,
    language: "go",
    startLine: 10,
    endLine: 20,
    source: "",
    calls,
  };
}

describe("parseExpand", () => {
  test("splits a chain on > and trims", () => {
    expect(parseExpand("retry.Do > backoff.Next")).toEqual(["retry.Do", "backoff.Next"]);
    expect(parseExpand("  Publish ")).toEqual(["Publish"]);
    expect(parseExpand(" > ")).toEqual([]);
  });
});

describe("symbolMatches", () => {
  const fn = "github.com/x/internal/tours.NewStore";
  const method = "(*github.com/x/internal/tours.Store).Save";
  test("every way of writing a function or method", () => {
    for (const q of ["NewStore", "tours.NewStore", "internal/tours.NewStore", fn]) {
      expect(symbolMatches(fn, q)).toBe(true);
    }
    for (const q of ["Save", "Store.Save", "tours.Store.Save", "(*Store).Save", "tours.(*Store).Save", "(*tours.Store).Save", method]) {
      expect(symbolMatches(method, q)).toBe(true);
    }
  });
  test("near misses", () => {
    for (const q of ["Store", "tours", "ewStore", "notes.NewStore", "", "NewStore.x"]) {
      expect(symbolMatches(fn, q)).toBe(false);
    }
    for (const q of ["AutoSave", "Stop.Save", "notes.Store.Save", "(*Store)"]) {
      expect(symbolMatches(method, q)).toBe(false);
    }
  });
});

describe("findCall", () => {
  const calls = [
    call("c1", "retry.Do", "github.com/x/retry.Do"),
    call("c2", "Publish", "(*github.com/x/pubsub.Client).Publish"),
    call("c3", "retry.Do", "github.com/x/retry.Do"),
    call("c4", "log.Printf"),
  ];

  test("exact display name, first occurrence", () => {
    expect(findCall(calls, "retry.Do")?.id).toBe("c1");
  });
  test("#n picks a later occurrence", () => {
    expect(findCall(calls, "retry.Do#2")?.id).toBe("c3");
    expect(findCall(calls, "retry.Do#3")).toBeUndefined();
  });
  test("bare name finds a qualified site", () => {
    expect(findCall(calls, "Do")?.id).toBe("c1");
  });
  test("qualified name finds a site shown bare, through its target", () => {
    expect(findCall(calls, "pubsub.Publish")?.id).toBe("c2");
    expect(findCall(calls, "Client.Publish")?.id).toBe("c2");
  });
  test("no match", () => {
    expect(findCall(calls, "Nope")).toBeUndefined();
  });
});

describe("matchFile", () => {
  const files = ["/p/internal/pubsub/retry.go", "/p/cmd/retry.go", "/p/internal/pubsub/pubsub.go"];
  test("suffix match, shortest wins", () => {
    expect(matchFile(files, "pubsub/retry.go")).toBe("/p/internal/pubsub/retry.go");
    expect(matchFile(files, "retry.go")).toBe("/p/cmd/retry.go");
    expect(matchFile(files, "./internal/pubsub/pubsub.go")).toBe("/p/internal/pubsub/pubsub.go");
  });
  test("absolute path and misses", () => {
    expect(matchFile(files, "/p/cmd/retry.go")).toBe("/p/cmd/retry.go");
    expect(matchFile(files, "nope.go")).toBeNull();
  });
});

describe("buildTree", () => {
  const root = frame("root", [call("a", "retry.Do", "x/retry.Do"), call("b", "metrics.Count")]);
  const retryDo = frame("retry", [call("n", "backoff.Next"), call("f", "fn")]);
  const fetched: string[] = [];
  const fetchBody = async (id: string) => {
    fetched.push(id);
    if (id === "a") return retryDo;
    throw new Error("unknown call " + id);
  };

  test("siblings and a nested chain share the tree", async () => {
    fetched.length = 0;
    const { tree, missing } = await buildTree(
      root,
      ["retry.Do", "retry.Do > backoff.Next", "metrics.Count"],
      fetchBody,
    );
    expect(missing).toEqual([]);
    expect(Object.keys(tree.expansions).sort()).toEqual(["a", "b"]);
    expect(Object.keys(tree.expansions.a.expansions)).toEqual(["n"]);
    expect(tree.expansions.a.choice).toBe(0);
    // The body of retry.Do is fetched once, for the chain only.
    expect(fetched).toEqual(["a"]);
  });

  test("a name that matches nothing is reported, the rest still opens", async () => {
    const { tree, missing } = await buildTree(root, ["Gone", "retry.Do > Gone", "metrics.Count"], fetchBody);
    expect(missing).toEqual(["Gone", "retry.Do > Gone"]);
    // retry.Do itself was reached on the way to the missing step.
    expect(Object.keys(tree.expansions).sort()).toEqual(["a", "b"]);
  });

  test("nothing to expand is the empty slice", async () => {
    const { tree } = await buildTree(root, [], fetchBody);
    expect(tree.expansions).toEqual({});
  });
});

describe("resolveStop", () => {
  const resolvers = {
    fetchSymbol: async (name: string) => {
      if (name === "Publish") return frame("pubsub.Publish", [call("a", "retry.Do")]);
      throw new Error("404");
    },
    fetchFiles: async () => ["/p/internal/pubsub/retry.go"],
    fetchBody: async () => {
      throw new Error("unused");
    },
  };

  test("symbol stop carries where its card goes and the tree it opens", async () => {
    const r = await resolveStop({ symbol: "Publish", expand: ["retry.Do"] }, resolvers);
    expect(r.kind).toBe("symbol");
    if (r.kind !== "symbol") return;
    expect(r.targetId).toBe("pubsub.Publish");
    expect(r.file).toBe("/p/pubsub.Publish.go");
    expect(r.line).toBe(10);
    expect(Object.keys(r.tree.expansions)).toEqual(["a"]);
  });

  test("unknown symbol is unresolved with a reason, not a throw", async () => {
    const r = await resolveStop({ symbol: "Nope" }, resolvers);
    expect(r).toEqual({ kind: "unresolved", reason: 'no symbol matches "Nope"' });
  });

  test("file stop resolves the path and defaults the line", async () => {
    expect(await resolveStop({ file: "pubsub/retry.go", line: 41 }, resolvers)).toEqual({
      kind: "file",
      file: "/p/internal/pubsub/retry.go",
      line: 41,
    });
    expect(await resolveStop({ file: "retry.go" }, resolvers)).toMatchObject({ kind: "file", line: 1 });
    expect(await resolveStop({ file: "zzz.go" }, resolvers)).toMatchObject({ kind: "unresolved" });
  });

  test("a stop that names nothing", async () => {
    expect(await resolveStop({}, resolvers)).toMatchObject({ kind: "unresolved" });
  });
});
