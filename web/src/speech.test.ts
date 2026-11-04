import { describe, expect, test } from "bun:test";
import { spokenDescription, spokenName, stopSpeechText } from "./speech";

describe("spokenName", () => {
  test("drops the package path and receiver punctuation", () => {
    expect(spokenName("github.com/x/pubsub.(*Client).Publish")).toBe("Client dot Publish");
    expect(spokenName("pubsub.(*Client).Publish")).toBe("Client dot Publish");
    expect(spokenName("(*T).M")).toBe("T dot M");
  });
  test("keeps a short dotted name", () => {
    expect(spokenName("retry.Do")).toBe("retry dot Do");
    expect(spokenName("validateName")).toBe("validateName");
  });
  test("ignores a workspace alias", () => {
    expect(spokenName("billing::pubsub.Publish")).toBe("pubsub dot Publish");
  });
});

describe("spokenDescription", () => {
  test("reads refs as names and strips code ticks", () => {
    expect(spokenDescription("Entry point — see [[pubsub.(*Client).Publish]] and `ctx`.")).toBe(
      "Entry point — see Client dot Publish and ctx.",
    );
  });
  test("reads a file ref as its basename", () => {
    expect(spokenDescription("The table lives in [[file:internal/pubsub/retry.go]].")).toBe(
      "The table lives in retry dot go.",
    );
  });
  test("collapses whitespace", () => {
    expect(spokenDescription("a\n\n  b")).toBe("a b");
  });
});

describe("stopSpeechText", () => {
  test("symbol stop: position, name, description", () => {
    expect(
      stopSpeechText({ symbol: "retry.Do", description: "The loop." }, 1, 3),
    ).toBe("Stop 2 of 3: retry dot Do. The loop.");
  });
  test("file stop with a line", () => {
    expect(stopSpeechText({ file: "internal/pubsub/retry.go", line: 41 }, 0, 1)).toBe(
      "Stop 1 of 1: retry dot go, line 41.",
    );
  });
});
