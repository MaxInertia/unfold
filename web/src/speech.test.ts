import { describe, expect, test } from "bun:test";
import { parseAlignment } from "./api";
import { charAtFraction, charAtTime, spokenDescription, spokenName, stopSpeechText } from "./speech";

describe("charAtTime", () => {
  // "hello" at 100ms a character.
  const al = [0, 100, 200, 300, 400];
  test("the last character that has started", () => {
    expect(charAtTime(al, 0)).toBe(0);
    expect(charAtTime(al, 99)).toBe(0);
    expect(charAtTime(al, 100)).toBe(1);
    expect(charAtTime(al, 250)).toBe(2);
    expect(charAtTime(al, 400)).toBe(4);
    expect(charAtTime(al, 9999)).toBe(4);
  });
  test("before the first character is the first", () => {
    expect(charAtTime([500, 600], 0)).toBe(0);
  });
  test("a time shared by several units (an emoji's two) resolves to the last", () => {
    expect(charAtTime([0, 250, 250, 500], 250)).toBe(2);
  });
});

test("charAtFraction scales position to text length, and is 0 with no duration", () => {
  expect(charAtFraction(2, 4, 100)).toBe(50);
  expect(charAtFraction(4, 4, 100)).toBe(100);
  expect(charAtFraction(5, 4, 100)).toBe(100);
  expect(charAtFraction(1, 0, 100)).toBe(0);
  expect(charAtFraction(1, NaN, 100)).toBe(0);
});

describe("parseAlignment", () => {
  test("the header as the server writes it", () => {
    expect(parseAlignment("[0,100,200]")).toEqual([0, 100, 200]);
  });
  test("anything else is no alignment", () => {
    expect(parseAlignment(null)).toBeNull();
    expect(parseAlignment("")).toBeNull();
    expect(parseAlignment("[]")).toBeNull();
    expect(parseAlignment("not json")).toBeNull();
    expect(parseAlignment('["a"]')).toBeNull();
    expect(parseAlignment("{}")).toBeNull();
  });
});

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
