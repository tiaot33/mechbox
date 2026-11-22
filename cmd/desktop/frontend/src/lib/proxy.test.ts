import { describe, expect, it } from "vitest";
import { formatSystemProxy } from "./proxy";

describe("formatSystemProxy", () => {
  it("returns empty string when no system proxy is configured", () => {
    expect(formatSystemProxy()).toBe("");
    expect(formatSystemProxy({})).toBe("");
    expect(formatSystemProxy({ http: " ", https: "", socks: "" })).toBe("");
  });

  it("collapses identical HTTP and HTTPS proxies", () => {
    expect(formatSystemProxy({ http: "http://127.0.0.1:7890", https: "http://127.0.0.1:7890" })).toBe("http://127.0.0.1:7890");
    expect(
      formatSystemProxy({
        http: "http://127.0.0.1:7890",
        https: "http://127.0.0.1:7890",
        socks: "socks5://127.0.0.1:7890",
      }),
    ).toBe("http://127.0.0.1:7890 · socks5://127.0.0.1:7890");
  });

  it("labels distinct HTTP and HTTPS proxies separately", () => {
    expect(formatSystemProxy({ http: "http://127.0.0.1:7890", https: "http://127.0.0.1:7891" })).toBe(
      "HTTP http://127.0.0.1:7890 · HTTPS http://127.0.0.1:7891",
    );
    expect(formatSystemProxy({ https: "http://127.0.0.1:7890" })).toBe("HTTPS http://127.0.0.1:7890");
  });

  it("shows SOCKS proxy alone when only SOCKS is enabled", () => {
    expect(formatSystemProxy({ socks: "socks5://127.0.0.1:1080" })).toBe("socks5://127.0.0.1:1080");
  });
});
