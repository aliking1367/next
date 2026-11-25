import { TOP_UP_MONTH_DAYS, topUpCost } from "./accountTopUp";

/** An admin's limits as the invoice compares them before and after a save. */
export type AdminLimitsSnapshot = {
	/** Users the admin may have; null is unlimited. */
	usersLimit: number | null;
	/** Data limit in GB; null is unlimited. */
	dataGB: number | null;
	/** Expiry as unix seconds; null or 0 is no expiry. */
	expire: number | null;
};

export type InvoicePrices = {
	/** Price of one user for a 30-day month. */
	userMonthly: number;
	/** Price of one GB of data. */
	perGB: number;
};

export type InvoiceLine =
	| { kind: "renew"; users: number; seconds: number; amount: number }
	| { kind: "users"; users: number; seconds: number; amount: number }
	| { kind: "data"; gb: number; amount: number };

export type Invoice = {
	lines: InvoiceLine[];
	total: number;
	expire: number | null;
};

const MONTH_SECONDS = TOP_UP_MONTH_DAYS * 86400;

const positive = (value: number | null | undefined) =>
	typeof value === "number" && Number.isFinite(value) && value > 0 ? value : 0;

/**
 * What a save added to an admin and what it costs:
 * - renew: the users the admin already had, for the time the expiry moved
 *   forward (counted from now when the old expiry had already passed);
 * - users: users added, from now until the new expiry (a month when the
 *   admin has no expiry);
 * - data: GB added.
 * Returns null when nothing was added.
 */
export const buildAdminInvoice = (
	before: AdminLimitsSnapshot,
	after: AdminLimitsSnapshot,
	prices: InvoicePrices,
	nowSeconds: number,
): Invoice | null => {
	const lines: InvoiceLine[] = [];
	const oldUsers = positive(before.usersLimit);
	const newUsers = positive(after.usersLimit);
	const oldExpire = positive(before.expire);
	const newExpire = positive(after.expire);

	if (oldExpire && newExpire && oldUsers) {
		const seconds = newExpire - Math.max(oldExpire, nowSeconds);
		if (seconds > 0) {
			lines.push({
				kind: "renew",
				users: oldUsers,
				seconds,
				amount: topUpCost(oldUsers, prices.userMonthly, seconds),
			});
		}
	}

	// Going from unlimited to a number is not an addition.
	const addedUsers =
		after.usersLimit === null || before.usersLimit === null
			? 0
			: newUsers - oldUsers;
	if (addedUsers > 0) {
		const seconds = newExpire ? newExpire - nowSeconds : MONTH_SECONDS;
		if (seconds > 0) {
			lines.push({
				kind: "users",
				users: addedUsers,
				seconds,
				amount: topUpCost(addedUsers, prices.userMonthly, seconds),
			});
		}
	}

	const addedGB =
		after.dataGB === null || before.dataGB === null
			? 0
			: positive(after.dataGB) - positive(before.dataGB);
	if (addedGB > 0) {
		lines.push({
			kind: "data",
			gb: addedGB,
			amount: Math.round(addedGB * positive(prices.perGB)),
		});
	}

	if (!lines.length) {
		return null;
	}
	return {
		lines,
		total: lines.reduce((sum, line) => sum + line.amount, 0),
		expire: newExpire || null,
	};
};

/** Sum of per-service limits; null when any service is unlimited. */
export const sumServiceLimits = (
	values: Array<number | null | undefined>,
): number | null => {
	if (!values.length) {
		return null;
	}
	let total = 0;
	for (const value of values) {
		if (value === null || value === undefined || value <= 0) {
			return null;
		}
		total += value;
	}
	return total;
};
