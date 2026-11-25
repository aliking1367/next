/** Days in the billing month the top-up price is quoted for. */
export const TOP_UP_MONTH_DAYS = 30;

export type RemainingTime = {
	totalSeconds: number;
	days: number;
	hours: number;
	minutes: number;
};

/** Time left until `expire` (unix seconds); zero once it has passed. */
export const remainingTime = (expire: number, nowMs: number): RemainingTime => {
	const totalSeconds = Math.max(0, Math.floor(expire - nowMs / 1000));
	return {
		totalSeconds,
		days: Math.floor(totalSeconds / 86400),
		hours: Math.floor((totalSeconds % 86400) / 3600),
		minutes: Math.floor((totalSeconds % 3600) / 60),
	};
};

/**
 * Price of adding `users` users for the time left on the account, when one
 * user costs `monthlyPrice` for a 30-day month. Counted by the hour: a part
 * hour is billed as a full one, and the total is rounded to the toman.
 */
export const topUpCost = (
	users: number,
	monthlyPrice: number,
	remainingSeconds: number,
): number => {
	if (!(users > 0) || !(monthlyPrice > 0) || !(remainingSeconds > 0)) {
		return 0;
	}
	const hours = Math.ceil(remainingSeconds / 3600);
	return Math.round((users * monthlyPrice * hours) / (TOP_UP_MONTH_DAYS * 24));
};

/** Reads a whole number typed with Latin, Persian or Arabic digits and separators. */
export const parseAmount = (value: string): number => {
	const latin = value
		.replace(/[۰-۹]/g, (d) => String(d.charCodeAt(0) - 0x06f0))
		.replace(/[٠-٩]/g, (d) => String(d.charCodeAt(0) - 0x0660))
		.replace(/[^0-9]/g, "");
	if (!latin) {
		return 0;
	}
	const parsed = Number(latin.slice(0, 15));
	return Number.isFinite(parsed) ? parsed : 0;
};
