import { describe, expect, it } from "vitest";
import { niceScale } from "./components/charts";
import { ago, deliveries, ms, pct, span, splitList, until, uptimeTone } from "./lib";
import { parseRoute } from "./router";
import type { Notification } from "./types";

describe("format", () => {
  const now = Date.parse("2026-10-02T12:00:00Z");
  it("ago and until", () => {
    expect(ago("2026-10-02T11:59:30Z", now)).toBe("just now");
    expect(ago("2026-10-02T09:00:00Z", now)).toBe("3h ago");
    expect(until("2026-10-02T12:30:00Z", now)).toBe("in 30m");
  });
  it("span", () => {
    expect(span(45)).toBe("45 s");
    expect(span(3600)).toBe("1 h");
    expect(span(5400)).toBe("1.5 h");
    expect(span(86400 * 3)).toBe("3 days");
  });
  it("ms and pct", () => {
    expect(ms(0)).toBe("—");
    expect(ms(5.47)).toBe("5.5 ms");
    expect(ms(194.9)).toBe("195 ms");
    expect(ms(1250)).toBe("1.3 s");
    expect(pct(-1)).toBe("—");
    expect(pct(100)).toBe("100%");
    expect(pct(99.95)).toBe("99.95%");
    expect(pct(97.08)).toBe("97.1%");
    expect(uptimeTone(99.9)).toBe("good");
    expect(uptimeTone(97)).toBe("warn");
    expect(uptimeTone(80)).toBe("bad");
  });
  it("splitList", () => {
    expect(splitList("a, b  c,,")).toEqual(["a", "b", "c"]);
  });
});

describe("niceScale", () => {
  it("rounds up to clean ticks", () => {
    expect(niceScale(194)).toEqual({ max: 200, step: 50 });
    expect(niceScale(188.1)).toEqual({ max: 200, step: 50 });
    expect(niceScale(7.3)).toEqual({ max: 8, step: 2 });
    expect(niceScale(0)).toEqual({ max: 1, step: 0.25 });
  });
});

describe("deliveries", () => {
  const n = (detail: string) => ({ detail }) as Notification;
  it("parses the delivery list and ignores plain text", () => {
    expect(deliveries(n('[{"target":"Phone","state":"sent"}]'))).toEqual([
      { target: "Phone", state: "sent" },
    ]);
    expect(deliveries(n("Same as one sent in the last 10 min"))).toEqual([]);
  });
});

describe("routes", () => {
  it("parses paths", () => {
    expect(parseRoute("/")).toEqual({ page: "home" });
    expect(parseRoute("/checks/12")).toEqual({ page: "check", id: 12 });
    expect(parseRoute("/checks/abc")).toEqual({ page: "home" });
    expect(parseRoute("/status")).toEqual({ page: "status" });
    expect(parseRoute("/notifications")).toEqual({ page: "notifications" });
  });
});
