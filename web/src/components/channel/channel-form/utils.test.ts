import { expect, it } from "vitest";

import { parseEndpoints } from "./utils";

it("returns an empty object for an empty string", () => {
  expect(parseEndpoints("")).toEqual({});
});

it("returns an empty object when the stored value is the JSON literal null", () => {
  // Some channel rows have `endpoints` stored as the 4-char string "null" rather than
  // an empty string. JSON.parse("null") succeeds and returns null, so parseEndpoints
  // must not trust the parsed result blindly, or callers indexing into it (e.g.
  // channels/page.tsx) crash with "Cannot read properties of null".
  expect(parseEndpoints("null")).toEqual({});
});

it("returns an empty object for a JSON array", () => {
  expect(parseEndpoints("[]")).toEqual({});
});

it("returns the parsed object for a valid endpoint config", () => {
  expect(parseEndpoints('{"chat_completions":"/v1/chat/completions"}')).toEqual({
    chat_completions: "/v1/chat/completions",
  });
});
