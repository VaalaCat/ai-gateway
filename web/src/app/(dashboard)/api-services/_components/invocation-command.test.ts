import { execFileSync } from "node:child_process";

import { describe, expect, it } from "vitest";

import {
	buildInvocationCommand,
	buildInvocationCommands,
	quotePOSIXShell,
} from "./invocation-command";

function shellArguments(command: string, env: Record<string, string> = {}) {
  const printable = command.replace(/^(curl|websocat)\b/, "printf '<%s>\\n'");
  return execFileSync("/bin/sh", ["-c", printable], {
    encoding: "utf8",
    env: { ...process.env, ...env },
  }).match(/(?<=<)[\s\S]*?(?=>\n|>$)/g) ?? [];
}

describe("quotePOSIXShell", () => {
  it("keeps quotes, newlines, dollars, and semicolons inside one shell word", () => {
    const value = "line one's\n$HOME; echo escaped";
    const output = execFileSync("/bin/sh", ["-c", `printf '%s' ${quotePOSIXShell(value)}`], {
      encoding: "utf8",
    });
    expect(output).toBe(value);
  });
});

describe("buildInvocationCommand", () => {
	it("ignores every example Authorization header and injects exactly one Gateway Authorization", () => {
		const commands = buildInvocationCommands({
			origin: "https://gateway.example",
			serviceSlug: "chat",
			routeSlug: "live",
			protocols: ["http", "websocket"],
			example: {
				method: "POST",
				subpath: "",
				query: "",
				headers: {
					Authorization: "Bearer internal-one",
					authorization: "Bearer internal-two",
					"X-Test": "1",
				},
				body: "payload",
			},
			token: "gateway-token",
		});

		expect(shellArguments(commands[0]!.command)).toEqual([
			"--request",
			"POST",
			"--url",
			"https://gateway.example/v1/api/chat/live",
			"--header",
			"Authorization: Bearer gateway-token",
			"--header",
			"X-Test: 1",
			"--data-raw",
			"payload",
		]);
		expect(shellArguments(commands[1]!.command)).toEqual([
			"--header",
			"Authorization: Bearer gateway-token",
			"--header",
			"X-Test: 1",
			"wss://gateway.example/v1/api/chat/live",
		]);
	});

	it("keeps both curl and websocat commands available for a dual-protocol Route", () => {
		const commands = buildInvocationCommands({
			origin: "https://gateway.example",
			serviceSlug: "chat",
			routeSlug: "live",
			protocols: ["http", "websocket"],
			example: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
			token: "token",
		});

		expect(commands.map((command) => command.kind)).toEqual(["curl", "websocat"]);
		expect(commands[0]?.publicUrl).toMatch(/^https:/);
		expect(commands[1]?.publicUrl).toMatch(/^wss:/);
	});
  it("builds a GET curl URL with escaped subpath and duplicate query order", () => {
    const result = buildInvocationCommand({
      origin: "https://gateway.example/",
      serviceSlug: "weather",
      routeSlug: "forecast",
      protocols: ["http"],
      example: {
        method: "GET",
        subpath: "/cities/New York/report%2Fdaily",
        query: "unit=c&unit=f&lang=zh",
        headers: {},
        body: "",
      },
      token: "production-token",
    });

    expect(result).toEqual({
      kind: "curl",
      publicUrl: "https://gateway.example/v1/api/weather/forecast/cities/New%20York/report%2Fdaily?unit=c&unit=f&lang=zh",
      command: "curl --request 'GET' --url 'https://gateway.example/v1/api/weather/forecast/cities/New%20York/report%2Fdaily?unit=c&unit=f&lang=zh' --header 'Authorization: Bearer production-token'",
    });
    expect(shellArguments(result.command)).toEqual([
      "--request",
      "GET",
      "--url",
      result.publicUrl,
      "--header",
      "Authorization: Bearer production-token",
    ]);
  });

  it("preserves internal empty path segments and a trailing slash", () => {
    const result = buildInvocationCommand({
      origin: "https://gateway.example",
      serviceSlug: "weather",
      routeSlug: "forecast",
      protocols: ["http"],
      example: {
        method: "GET",
        subpath: "reports//daily/",
        query: "",
        headers: {},
        body: "",
      },
      token: "production-token",
    });

    expect(result.publicUrl).toBe(
      "https://gateway.example/v1/api/weather/forecast/reports//daily/",
    );
    expect(shellArguments(result.command)).toContain(result.publicUrl);
  });

  it("encodes dot and encoded-slash segments without collapsing the public path", () => {
    const result = buildInvocationCommand({
      origin: "https://gateway.example",
      serviceSlug: "weather",
      routeSlug: "forecast",
      protocols: ["http"],
      example: {
        method: "GET",
        subpath: "reports/../daily%2Fhourly//",
        query: "unit=c&unit=f&lang=zh",
        headers: {},
        body: "",
      },
      token: "production-token",
    });

    expect(result.publicUrl).toBe(
      "https://gateway.example/v1/api/weather/forecast/reports/%2E%2E/daily%2Fhourly//?unit=c&unit=f&lang=zh",
    );
    expect(shellArguments(result.command)).toContain(result.publicUrl);
  });

  it("builds a POST curl command whose token, headers, and body stay in their argument boundaries", () => {
    const result = buildInvocationCommand({
      origin: "http://gateway.example",
      serviceSlug: "events",
      routeSlug: "append",
      protocols: ["http"],
      example: {
        method: "POST",
        subpath: "",
        query: "",
        headers: {
          "Content-Type": "application/json",
          "X-Note": "one's\n$HOME; echo header",
        },
        body: "{\"message\":\"one's\\n$HOME; echo body\"}",
      },
      token: "tok'en\n$HOME; echo token",
    });

    expect(result.kind).toBe("curl");
    expect(shellArguments(result.command)).toEqual([
      "--request",
      "POST",
      "--url",
      "http://gateway.example/v1/api/events/append",
      "--header",
      "Authorization: Bearer tok'en\n$HOME; echo token",
      "--header",
      "Content-Type: application/json",
      "--header",
      "X-Note: one's\n$HOME; echo header",
      "--data-raw",
      "{\"message\":\"one's\\n$HOME; echo body\"}",
    ]);
  });

  it("keeps the API token template runnable through environment expansion", () => {
    const result = buildInvocationCommand({
      origin: "https://gateway.example",
      serviceSlug: "weather",
      routeSlug: "forecast",
      protocols: ["http"],
      example: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
      token: "${API_TOKEN}",
    });

    expect(result.command).toContain("${API_TOKEN}");
    expect(shellArguments(result.command, { API_TOKEN: "runtime token's\n$HOME; safe" })).toContain(
      "Authorization: Bearer runtime token's\n$HOME; safe",
    );
  });

  it.each([
    ["https://gateway.example", "wss://gateway.example/v1/api/chat/live"],
    ["http://gateway.example", "ws://gateway.example/v1/api/chat/live"],
  ])("builds a websocat command for %s without leaking a subprotocol into shell syntax", (origin, publicUrl) => {
    const result = buildInvocationCommand({
      origin,
      serviceSlug: "chat",
      routeSlug: "live",
      protocols: ["websocket"],
      example: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
      token: "token",
    });
    const subprotocol = "chat'v1\n$HOME; echo escaped";
    const withSubprotocol = buildInvocationCommand({
      origin,
      serviceSlug: "chat",
      routeSlug: "live",
      protocols: ["websocket"],
      example: {
        method: "GET",
        subpath: "",
        query: "",
        headers: { "Sec-WebSocket-Protocol": subprotocol },
        body: "",
      },
      token: "token",
    });

    expect(result).toMatchObject({ kind: "websocat", publicUrl });
    expect(shellArguments(withSubprotocol.command)).toEqual([
      "--header",
      "Authorization: Bearer token",
      "--protocol",
      subprotocol,
      publicUrl,
    ]);
  });

  it("rejects a non-origin path while normalizing the root path", () => {
    expect(() => buildInvocationCommand({
      origin: "https://gateway.example/control",
      serviceSlug: "weather",
      routeSlug: "forecast",
      protocols: ["http"],
      example: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
      token: "token",
    })).toThrow("origin must not contain a path");

    expect(buildInvocationCommand({
      origin: "https://gateway.example/",
      serviceSlug: "weather",
      routeSlug: "forecast",
      protocols: ["http"],
      example: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
      token: "token",
    }).publicUrl).toBe("https://gateway.example/v1/api/weather/forecast");
  });

  it.each([
    "ftp://gateway.example",
    "https://user:secret@gateway.example",
    "https://gateway.example/control",
    "https://gateway.example?mode=debug",
    "https://gateway.example#debug",
  ])("rejects unsafe invocation origin %s", (origin) => {
    expect(() => buildInvocationCommand({
      origin,
      serviceSlug: "weather",
      routeSlug: "forecast",
      protocols: ["http"],
      example: { method: "GET", subpath: "", query: "", headers: {}, body: "" },
      token: "token",
    })).toThrow();
  });
});
