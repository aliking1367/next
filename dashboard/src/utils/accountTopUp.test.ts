import { describe, expect, it } from "vitest";
import { parseAmount, remainingTime, topUpCost } from "./accountTopUp";

describe("remainingTime", () => {
	it("splits the time left into days, hours and minutes", () => {
		const now = 1_000_000_000_000;
		const expire = now / 1000 + 12 * 86400 + 5 * 3600 + 30 * 60 + 15;
		expect(remainingTime(expire, now)).toEqual({
			totalSeconds: 12 * 86400 + 5 * 3600 + 30 * 60 + 15,
			days: 12,
			hours: 5,
			minutes: 30,
		});
	});

	it("is zero once the account has expired", () => {
		expect(remainingTime(100, 200_000).totalSeconds).toBe(0);
	});
});

describe("topUpCost", () => {
	it("charges a full month for 30 days left", () => {
		expect(topUpCost(10, 75000, 30 * 86400)).toBe(750000);
	});

	it("charges by the hour for the time left", () => {
		// 15 days and 12 hours = 372 of 720 hours.
		expect(topUpCost(1, 75000, 15 * 86400 + 12 * 3600)).toBe(38750);
		// A part hour counts as a full one.
		expect(topUpCost(1, 72000, 3600 + 1)).toBe(200);
	});

	it("is zero without users, price or time", () => {
		expect(topUpCost(0, 75000, 86400)).toBe(0);
		expect(topUpCost(5, 0, 86400)).toBe(0);
		expect(topUpCost(5, 75000, 0)).toBe(0);
	});
});

describe("parseAmount", () => {
	it("reads Persian digits and separators", () => {
		expect(parseAmount("۷۵٬۰۰۰")).toBe(75000);
		expect(parseAmount("75,000")).toBe(75000);
		expect(parseAmount("")).toBe(0);
	});
});
