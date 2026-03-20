import { describe, it, expect } from "vitest";
import { cn, formatCurrency, formatCompact, maskSecret, relativeTime } from "./utils";

describe("cn", () => {
  it("merges class names", () => {
    expect(cn("a", "b")).toBe("a b");
  });

  it("handles conditional classes", () => {
    const isHidden = false;
    expect(cn("base", isHidden && "hidden", "end")).toBe("base end");
  });

  it("deduplicates tailwind conflicts", () => {
    expect(cn("px-4", "px-8")).toBe("px-8");
  });
});

describe("formatCurrency", () => {
  it("formats USD by default", () => {
    expect(formatCurrency(1234.56)).toBe("$1,234.56");
  });

  it("handles string input", () => {
    expect(formatCurrency("0.005")).toBe("$0.005");
  });

  it("shows at least 2 decimal places", () => {
    expect(formatCurrency(100)).toBe("$100.00");
  });
});

describe("formatCompact", () => {
  it("returns raw number under 1K", () => {
    expect(formatCompact(999)).toBe("999");
  });

  it("formats thousands", () => {
    expect(formatCompact(1500)).toBe("1.5K");
  });

  it("formats millions", () => {
    expect(formatCompact(2_500_000)).toBe("2.5M");
  });
});

describe("maskSecret", () => {
  it("masks middle of a key", () => {
    const key = "ak_live_abcdef1234567890abcdef";
    const masked = maskSecret(key);
    expect(masked).toContain("ak_live_");
    expect(masked).toContain("...");
    expect(masked).not.toBe(key);
  });

  it("returns short keys unchanged", () => {
    expect(maskSecret("short")).toBe("short");
  });
});

describe("relativeTime", () => {
  it("returns 'just now' for recent timestamps", () => {
    const now = new Date();
    expect(relativeTime(now)).toBe("just now");
  });

  it("returns minutes ago", () => {
    const fiveMinAgo = new Date(Date.now() - 5 * 60 * 1000);
    expect(relativeTime(fiveMinAgo)).toBe("5m ago");
  });

  it("returns hours ago", () => {
    const threeHoursAgo = new Date(Date.now() - 3 * 60 * 60 * 1000);
    expect(relativeTime(threeHoursAgo)).toBe("3h ago");
  });

  it("returns days ago", () => {
    const twoDaysAgo = new Date(Date.now() - 2 * 24 * 60 * 60 * 1000);
    expect(relativeTime(twoDaysAgo)).toBe("2d ago");
  });

  it("handles string dates", () => {
    const recent = new Date(Date.now() - 10 * 60 * 1000).toISOString();
    expect(relativeTime(recent)).toBe("10m ago");
  });
});
