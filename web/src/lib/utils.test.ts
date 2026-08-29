import { describe, expect, it } from "vitest";
import { cn } from "./utils";

describe("cn", () => {
  it("merges conditional classes and resolves conflicting Tailwind utilities", () => {
    expect(cn("px-2", false && "hidden", "px-4", ["text-sm", { "font-bold": true }])).toBe(
      "px-4 text-sm font-bold",
    );
  });
});
