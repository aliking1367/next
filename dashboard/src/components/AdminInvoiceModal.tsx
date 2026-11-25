import {
	Button,
	FormControl,
	FormLabel,
	HStack,
	Input,
	InputGroup,
	InputRightAddon,
	Modal,
	ModalBody,
	ModalCloseButton,
	ModalContent,
	ModalFooter,
	ModalHeader,
	ModalOverlay,
	SimpleGrid,
	Text,
	Textarea,
	useClipboard,
	useToast,
	VStack,
} from "@chakra-ui/react";
import {
	ClipboardDocumentCheckIcon,
	ClipboardDocumentIcon,
	PaperAirplaneIcon,
	ShareIcon,
} from "@heroicons/react/24/outline";
import dayjs from "dayjs";
import { type FC, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetch } from "service/http";
import { parseAmount } from "utils/accountTopUp";
import {
	type AdminLimitsSnapshot,
	buildAdminInvoice,
	type InvoiceLine,
} from "utils/adminInvoice";

export type AdminInvoiceRequest = {
	username: string;
	adminId?: number;
	/** The admin's Telegram id; the invoice can be sent there by the panel bot. */
	telegramId?: number | null;
	before: AdminLimitsSnapshot;
	after: AdminLimitsSnapshot;
	/** When the save happened, unix seconds. */
	at: number;
};

// The calculator on My Account remembers the user price under the same key,
// so both start from the price the owner last used.
const STORAGE = {
	userMonthly: "next.topup.monthlyPrice",
	perGB: "next.invoice.perGB",
	payment: "next.invoice.payment",
};
const DEFAULT_USER_MONTHLY = 75000;

const readStored = (key: string): string => {
	try {
		return window.localStorage.getItem(key) ?? "";
	} catch {
		return "";
	}
};

const writeStored = (key: string, value: string) => {
	try {
		window.localStorage.setItem(key, value);
	} catch {
		// Remembering prices is only a convenience.
	}
};

const splitDuration = (seconds: number) => ({
	days: Math.floor(seconds / 86400),
	hours: Math.floor((seconds % 86400) / 3600),
});

// Shown after an admin's users, data or expiry is raised: an invoice for
// what was added, ready to copy or share with the admin.
export const AdminInvoiceModal: FC<{
	request: AdminInvoiceRequest | null;
	onClose: () => void;
}> = ({ request, onClose }) => {
	const { t, i18n } = useTranslation();
	const isFa = i18n.language?.startsWith("fa");
	const fmt = useMemo(() => {
		const formatter = new Intl.NumberFormat(isFa ? "fa-IR" : "en-US");
		return (value: number) => formatter.format(value);
	}, [isFa]);
	const formatDate = useMemo(() => {
		const formatter = new Intl.DateTimeFormat(
			isFa ? "fa-IR-u-ca-persian" : "en-GB",
			{
				year: "numeric",
				month: "2-digit",
				day: "2-digit",
				hour: "2-digit",
				minute: "2-digit",
			},
		);
		return (unix: number) => formatter.format(new Date(unix * 1000));
	}, [isFa]);

	const [userMonthly, setUserMonthly] = useState<number>(
		() => parseAmount(readStored(STORAGE.userMonthly)) || DEFAULT_USER_MONTHLY,
	);
	const [perGB, setPerGB] = useState<number>(() =>
		parseAmount(readStored(STORAGE.perGB)),
	);
	const [payment, setPayment] = useState<string>(() =>
		readStored(STORAGE.payment),
	);

	const invoice = useMemo(
		() =>
			request
				? buildAdminInvoice(
						request.before,
						request.after,
						{ userMonthly, perGB },
						request.at,
					)
				: null,
		[request, userMonthly, perGB],
	);
	const hasData = Boolean(invoice?.lines.some((line) => line.kind === "data"));
	const currency = t("myaccount.topUpCurrency");

	const generated = useMemo(() => {
		if (!request || !invoice) {
			return "";
		}
		const describeLine = (line: InvoiceLine) => {
			if (line.kind === "data") {
				return t("admins.invoice.lineData", {
					gb: fmt(line.gb),
					price: fmt(perGB),
				});
			}
			const { days, hours } = splitDuration(line.seconds);
			const period = hours
				? t("admins.invoice.periodDaysHours", {
						days: fmt(days),
						hours: fmt(hours),
					})
				: t("admins.invoice.periodDays", { days: fmt(days) });
			return t(
				line.kind === "renew"
					? "admins.invoice.lineRenew"
					: "admins.invoice.lineUsers",
				{ users: fmt(line.users), period },
			);
		};
		// The admin's id and the save time down to the minute: short to
		// quote, and different for every invoice of the same admin.
		const number = `${request.adminId ?? 0}-${dayjs.unix(request.at).format("YYMMDDHHmm")}`;
		const rows = [
			`🧾 ${t("admins.invoice.heading")}`,
			`${t("admins.invoice.number")}: ${number}`,
			`${t("admins.invoice.date")}: ${formatDate(request.at)}`,
			`${t("admins.invoice.admin")}: ${request.username}`,
			"━━━━━━━━━━━━",
			...invoice.lines.map(
				(line) =>
					`${line.kind === "renew" ? "🔄" : line.kind === "users" ? "➕" : "📦"} ${describeLine(line)}: ${fmt(line.amount)} ${currency}`,
			),
			"━━━━━━━━━━━━",
			`💰 ${t("admins.invoice.total")}: ${fmt(invoice.total)} ${currency}`,
		];
		if (invoice.expire) {
			rows.push(
				`📅 ${t("admins.invoice.validUntil")}: ${formatDate(invoice.expire)}`,
			);
		}
		if (payment.trim()) {
			rows.push(
				"",
				`💳 ${t("admins.invoice.paymentHeading")}:`,
				payment.trim(),
			);
		}
		return rows.join("\n");
	}, [request, invoice, payment, perGB, fmt, formatDate, currency, t]);

	const [message, setMessage] = useState(generated);
	useEffect(() => setMessage(generated), [generated]);

	const { hasCopied, onCopy } = useClipboard(message);
	const toast = useToast();
	const [sending, setSending] = useState(false);
	// The save this invoice belongs to, once sent, so a new invoice can be
	// sent again.
	const [sentAt, setSentAt] = useState<number | null>(null);
	const isSent = Boolean(request && sentAt === request.at);
	const canSendTelegram = Boolean(request?.telegramId);

	const sendTelegram = async () => {
		if (!request || sending) return;
		setSending(true);
		try {
			await fetch(
				`/admin/${encodeURIComponent(request.username)}/invoice/telegram`,
				{ method: "POST", body: { text: message } },
			);
			setSentAt(request.at);
			toast({
				status: "success",
				title: t("admins.invoice.sent", { username: request.username }),
				position: "top",
				duration: 3000,
			});
		} catch (error) {
			const detail = String(
				(error as { response?: { _data?: { detail?: unknown } } })?.response
					?._data?.detail ?? "",
			);
			const key = detail.startsWith("admin_has_no_telegram_id")
				? "admins.invoice.errorNoTelegram"
				: detail.startsWith("telegram_not_configured")
					? "admins.invoice.errorBotOff"
					: detail.startsWith("telegram_send_failed")
						? "admins.invoice.errorNotStarted"
						: "admins.invoice.errorGeneric";
			toast({
				status: "error",
				title: t(key),
				description:
					detail && key === "admins.invoice.errorGeneric" ? detail : undefined,
				position: "top",
				duration: 6000,
				isClosable: true,
			});
		} finally {
			setSending(false);
		}
	};
	const canShare = typeof navigator !== "undefined" && "share" in navigator;

	return (
		<Modal
			isOpen={Boolean(request && invoice)}
			onClose={onClose}
			isCentered
			size="lg"
			scrollBehavior="inside"
		>
			<ModalOverlay />
			<ModalContent mx={3}>
				<ModalHeader>{t("admins.invoice.title")}</ModalHeader>
				<ModalCloseButton />
				<ModalBody>
					<VStack align="stretch" spacing={3}>
						<Text fontSize="sm" color="panel.textSecondary">
							{t("admins.invoice.hint")}
						</Text>
						<SimpleGrid columns={{ base: 1, sm: hasData ? 2 : 1 }} spacing={3}>
							<FormControl>
								<FormLabel fontSize="xs" mb={1}>
									{t("myaccount.topUpMonthlyPrice")}
								</FormLabel>
								<InputGroup size="sm">
									<Input
										inputMode="numeric"
										dir="ltr"
										value={userMonthly ? fmt(userMonthly) : ""}
										onChange={(event) => {
											const next = parseAmount(event.target.value);
											setUserMonthly(next);
											if (next > 0)
												writeStored(STORAGE.userMonthly, String(next));
										}}
									/>
									<InputRightAddon>{currency}</InputRightAddon>
								</InputGroup>
							</FormControl>
							{hasData && (
								<FormControl>
									<FormLabel fontSize="xs" mb={1}>
										{t("admins.invoice.perGB")}
									</FormLabel>
									<InputGroup size="sm">
										<Input
											inputMode="numeric"
											dir="ltr"
											value={perGB ? fmt(perGB) : ""}
											onChange={(event) => {
												const next = parseAmount(event.target.value);
												setPerGB(next);
												writeStored(STORAGE.perGB, String(next));
											}}
										/>
										<InputRightAddon>{currency}</InputRightAddon>
									</InputGroup>
								</FormControl>
							)}
						</SimpleGrid>
						<FormControl>
							<FormLabel fontSize="xs" mb={1}>
								{t("admins.invoice.paymentLabel")}
							</FormLabel>
							<Textarea
								size="sm"
								rows={2}
								value={payment}
								placeholder={t("admins.invoice.paymentPlaceholder")}
								onChange={(event) => {
									setPayment(event.target.value);
									writeStored(STORAGE.payment, event.target.value);
								}}
							/>
						</FormControl>
						{!canSendTelegram && (
							<Text fontSize="xs" color="panel.textMuted">
								{t("admins.invoice.noTelegramHint")}
							</Text>
						)}
						<FormControl>
							<FormLabel fontSize="xs" mb={1}>
								{t("admins.invoice.preview")}
							</FormLabel>
							<Textarea
								value={message}
								onChange={(event) => setMessage(event.target.value)}
								rows={12}
								fontFamily="inherit"
								fontSize="sm"
							/>
						</FormControl>
					</VStack>
				</ModalBody>
				<ModalFooter>
					<HStack spacing={2} w="full" justify="flex-end" flexWrap="wrap">
						{canSendTelegram && (
							<Button
								leftIcon={<PaperAirplaneIcon width={18} />}
								colorScheme="blue"
								isLoading={sending}
								isDisabled={isSent}
								onClick={sendTelegram}
							>
								{isSent
									? t("admins.invoice.sentShort")
									: t("admins.invoice.sendTelegram")}
							</Button>
						)}
						{canShare && (
							<Button
								leftIcon={<ShareIcon width={18} />}
								variant="outline"
								onClick={() => {
									navigator.share({ text: message }).catch(() => undefined);
								}}
							>
								{t("admins.loginShare.share")}
							</Button>
						)}
						<Button
							colorScheme="primary"
							leftIcon={
								hasCopied ? (
									<ClipboardDocumentCheckIcon width={18} />
								) : (
									<ClipboardDocumentIcon width={18} />
								)
							}
							onClick={onCopy}
						>
							{hasCopied
								? t("admins.invoice.copied")
								: t("admins.invoice.copy")}
						</Button>
					</HStack>
				</ModalFooter>
			</ModalContent>
		</Modal>
	);
};
