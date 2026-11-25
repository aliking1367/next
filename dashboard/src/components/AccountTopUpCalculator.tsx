import {
	Box,
	FormControl,
	FormLabel,
	HStack,
	Icon,
	Input,
	InputGroup,
	InputRightAddon,
	SimpleGrid,
	Text,
} from "@chakra-ui/react";
import { CalculatorIcon } from "@heroicons/react/24/outline";
import dayjs from "dayjs";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	parseAmount,
	type RemainingTime,
	remainingTime,
	TOP_UP_MONTH_DAYS,
	topUpCost,
} from "utils/accountTopUp";

const PRICE_STORAGE_KEY = "next.topup.monthlyPrice";
const DEFAULT_MONTHLY_PRICE = 75000;

const readStoredPrice = (): number => {
	try {
		const stored = Number(window.localStorage.getItem(PRICE_STORAGE_KEY));
		return stored > 0 ? stored : DEFAULT_MONTHLY_PRICE;
	} catch {
		return DEFAULT_MONTHLY_PRICE;
	}
};

/** Re-renders every `intervalMs` so a countdown stays current. */
const useNow = (intervalMs: number) => {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const id = window.setInterval(() => setNow(Date.now()), intervalMs);
		return () => window.clearInterval(id);
	}, [intervalMs]);
	return now;
};

const useNumberFormat = () => {
	const { i18n } = useTranslation();
	return useMemo(() => {
		const locale = i18n.language?.startsWith("fa") ? "fa-IR" : "en-US";
		const formatter = new Intl.NumberFormat(locale);
		return (value: number) => formatter.format(value);
	}, [i18n.language]);
};

const useFormatRemaining = () => {
	const { t } = useTranslation();
	const fmt = useNumberFormat();
	return (left: RemainingTime) =>
		t("myaccount.remainingTimeValue", {
			days: fmt(left.days),
			hours: fmt(left.hours),
			minutes: fmt(left.minutes),
		});
};

/** Account expiry: the date and a live "x days, y hours, z minutes" countdown. */
export const AccountExpiryValue = ({ expire }: { expire?: number | null }) => {
	const { t } = useTranslation();
	const now = useNow(30_000);
	const formatRemaining = useFormatRemaining();
	if (!expire || expire <= 0) {
		return <Text fontWeight="semibold">{t("myaccount.unlimited")}</Text>;
	}
	const left = remainingTime(expire, now);
	return (
		<Text fontWeight="semibold">
			{dayjs.unix(expire).format("YYYY-MM-DD HH:mm")}{" "}
			<Text
				as="span"
				color={left.totalSeconds > 0 ? "panel.accent" : "red.400"}
			>
				(
				{left.totalSeconds > 0
					? t("myaccount.remainingTimeLeft", { time: formatRemaining(left) })
					: t("myaccount.accountExpired")}
				)
			</Text>
		</Text>
	);
};

/**
 * What adding users costs for the rest of the account's time: the price of
 * one user for a 30-day month, scaled by the hours left on the account.
 */
export const AccountTopUpCalculator = ({
	expire,
}: {
	expire?: number | null;
}) => {
	const { t } = useTranslation();
	const fmt = useNumberFormat();
	const formatRemaining = useFormatRemaining();
	const now = useNow(60_000);
	const [price, setPrice] = useState<number>(readStoredPrice);
	const [users, setUsers] = useState<number>(1);
	const [manualDays, setManualDays] = useState<number>(TOP_UP_MONTH_DAYS);

	const hasExpire = !!expire && expire > 0;
	const left = hasExpire
		? remainingTime(expire as number, now)
		: remainingTime(now / 1000 + manualDays * 86400, now);
	const total = topUpCost(users, price, left.totalSeconds);
	const perUser = topUpCost(1, price, left.totalSeconds);

	const updatePrice = (value: string) => {
		const next = parseAmount(value);
		setPrice(next);
		try {
			if (next > 0) {
				window.localStorage.setItem(PRICE_STORAGE_KEY, String(next));
			}
		} catch {
			// Remembering the price is only a convenience.
		}
	};

	return (
		<Box
			mt={4}
			p={3}
			borderWidth="1px"
			borderColor="panel.border"
			borderRadius="md"
		>
			<HStack spacing={2} mb={3}>
				<Icon
					as={CalculatorIcon}
					boxSize={4}
					color="panel.accent"
					aria-hidden="true"
				/>
				<Text fontSize="sm" fontWeight="semibold">
					{t("myaccount.topUpTitle")}
				</Text>
			</HStack>
			<SimpleGrid columns={{ base: 1, sm: hasExpire ? 2 : 3 }} spacing={3}>
				<FormControl>
					<FormLabel fontSize="xs" mb={1}>
						{t("myaccount.topUpMonthlyPrice")}
					</FormLabel>
					<InputGroup size="sm">
						<Input
							inputMode="numeric"
							dir="ltr"
							value={price ? fmt(price) : ""}
							onChange={(event) => updatePrice(event.target.value)}
						/>
						<InputRightAddon>{t("myaccount.topUpCurrency")}</InputRightAddon>
					</InputGroup>
				</FormControl>
				<FormControl>
					<FormLabel fontSize="xs" mb={1}>
						{t("myaccount.topUpUsers")}
					</FormLabel>
					<Input
						size="sm"
						inputMode="numeric"
						dir="ltr"
						value={users ? fmt(users) : ""}
						onChange={(event) =>
							setUsers(Math.min(parseAmount(event.target.value), 100000))
						}
					/>
				</FormControl>
				{!hasExpire && (
					<FormControl>
						<FormLabel fontSize="xs" mb={1}>
							{t("myaccount.topUpDays")}
						</FormLabel>
						<Input
							size="sm"
							inputMode="numeric"
							dir="ltr"
							value={manualDays ? fmt(manualDays) : ""}
							onChange={(event) =>
								setManualDays(Math.min(parseAmount(event.target.value), 3650))
							}
						/>
					</FormControl>
				)}
			</SimpleGrid>
			<Box mt={3} fontSize="sm">
				<Text color="panel.textSecondary">
					{t("myaccount.topUpRemaining")}:{" "}
					<Text as="span" fontWeight="semibold" color="panel.text">
						{left.totalSeconds > 0
							? formatRemaining(left)
							: t("myaccount.accountExpired")}
					</Text>
				</Text>
				<Text color="panel.textSecondary" mt={1}>
					{t("myaccount.topUpPerUser")}:{" "}
					<Text as="span" fontWeight="semibold" color="panel.text">
						{fmt(perUser)} {t("myaccount.topUpCurrency")}
					</Text>
				</Text>
				<Text mt={2} fontSize="lg" fontWeight="bold">
					{t("myaccount.topUpTotal")}:{" "}
					<Text as="span" color="panel.accent">
						{fmt(total)} {t("myaccount.topUpCurrency")}
					</Text>
				</Text>
				<Text mt={1} fontSize="xs" color="panel.textMuted">
					{t("myaccount.topUpHint", { days: fmt(TOP_UP_MONTH_DAYS) })}
				</Text>
			</Box>
		</Box>
	);
};
