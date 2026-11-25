import { describe, expect, it } from "vitest";
import { buildAdminInvoice, sumServiceLimits } from "./adminInvoice";

const DAY = 86400;
const NOW = 1_800_000_000;
const prices = { userMonthly: 75000, perGB: 1000 };

describe("buildAdminInvoice", () => {
	it("bills a renewal for the users the admin already had", () => {
		const invoice = buildAdminInvoice(
			{ usersLimit: 50, dataGB: null, expire: NOW + 2 * DAY },
			{ usersLimit: 50, dataGB: null, expire: NOW + 32 * DAY },
			prices,
			NOW,
		);
		expect(invoice?.lines).toEqual([
			{ kind: "renew", users: 50, seconds: 30 * DAY, amount: 3_750_000 },
		]);
		expect(invoice?.total).toBe(3_750_000);
	});

	it("renews from now when the old expiry has passed", () => {
		const invoice = buildAdminInvoice(
			{ usersLimit: 10, dataGB: null, expire: NOW - 5 * DAY },
			{ usersLimit: 10, dataGB: null, expire: NOW + 30 * DAY },
			prices,
			NOW,
		);
		expect(invoice?.total).toBe(750_000);
	});

	it("bills added users for the time left on the new expiry", () => {
		const invoice = buildAdminInvoice(
			{ usersLimit: 50, dataGB: null, expire: NOW + 15 * DAY },
			{ usersLimit: 60, dataGB: null, expire: NOW + 15 * DAY },
			prices,
			NOW,
		);
		expect(invoice?.lines).toEqual([
			{ kind: "users", users: 10, seconds: 15 * DAY, amount: 375_000 },
		]);
	});

	it("bills renewal, new users and data together", () => {
		const invoice = buildAdminInvoice(
			{ usersLimit: 20, dataGB: 100, expire: NOW + 10 * DAY },
			{ usersLimit: 30, dataGB: 300, expire: NOW + 40 * DAY },
			prices,
			NOW,
		);
		expect(invoice?.lines.map((line) => line.amount)).toEqual([
			1_500_000, 1_000_000, 200_000,
		]);
		expect(invoice?.total).toBe(2_700_000);
	});

	it("bills a new admin from now", () => {
		const invoice = buildAdminInvoice(
			{ usersLimit: 0, dataGB: 0, expire: null },
			{ usersLimit: 30, dataGB: null, expire: NOW + 30 * DAY },
			prices,
			NOW,
		);
		expect(invoice?.lines).toEqual([
			{ kind: "users", users: 30, seconds: 30 * DAY, amount: 2_250_000 },
		]);
	});

	it("is null when nothing was added", () => {
		const same = { usersLimit: 50, dataGB: 100, expire: NOW + DAY };
		expect(buildAdminInvoice(same, same, prices, NOW)).toBeNull();
		expect(
			buildAdminInvoice(
				same,
				{ usersLimit: 40, dataGB: 50, expire: NOW + DAY },
				prices,
				NOW,
			),
		).toBeNull();
	});
});

describe("buildAdminInvoice from unlimited", () => {
	it("does not bill limiting an unlimited admin", () => {
		expect(
			buildAdminInvoice(
				{ usersLimit: null, dataGB: null, expire: NOW + DAY },
				{ usersLimit: 30, dataGB: 100, expire: NOW + DAY },
				prices,
				NOW,
			),
		).toBeNull();
	});
});

describe("sumServiceLimits", () => {
	it("adds the limits and treats any unlimited service as unlimited", () => {
		expect(sumServiceLimits([10, 20])).toBe(30);
		expect(sumServiceLimits([10, null])).toBeNull();
		expect(sumServiceLimits([])).toBeNull();
	});
});
