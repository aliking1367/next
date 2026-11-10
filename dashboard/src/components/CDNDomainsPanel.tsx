import {
	Badge,
	Box,
	Button,
	FormControl,
	FormLabel,
	HStack,
	SimpleGrid,
	Spinner,
	Stack,
	Text,
	useClipboard,
	useColorModeValue,
	useDisclosure,
	useToast,
} from "@chakra-ui/react";
import { CloudIcon } from "@heroicons/react/24/outline";
import { AppDialog } from "components/dialogs/AppDialog";
import { ConfirmDialog } from "components/dialogs/ConfirmDialog";
import { Input } from "components/Input";
import { type FC, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "react-query";
import { fetch } from "service/http";

type CDNDomain = {
	id: number;
	domain: string;
	note: string;
	active: boolean;
	checked_at?: string;
	check_state: string;
	check_reason: string;
	check_status: number;
	iran_checked_at?: string;
	iran_open: number;
	iran_total: number;
	iran_error?: string;
};

type CDNDomainsView = {
	path_mode: boolean;
	active: string;
	origin_ip: string;
	origin_port: number;
	expression: string;
	domains: CDNDomain[] | null;
	check?: { state: string; reason: string; status: number };
	iran?: { open: number; total: number };
	moved?: number;
};

export const cdnDomainsQueryKey = "cdn-domains";

const useTT = () => {
	const { t } = useTranslation();
	return (key: string, fallback: string, options?: Record<string, unknown>) =>
		t(key, { defaultValue: fallback, ...options }) as string;
};

// reasonText explains a failed check through Cloudflare in the words of the
// Cloudflare setting that is wrong.
const reasonText = (
	tt: ReturnType<typeof useTT>,
	reason: string,
	status: number,
): string => {
	switch (reason) {
		case "dns":
			return tt(
				"cdn.reason.dns",
				"The domain has no DNS record yet (or it has not spread yet).",
			);
		case "not_proxied":
			return tt(
				"cdn.reason.notProxied",
				"The record is not proxied: turn the cloud orange, or this server's IP shows.",
			);
		case "challenge":
			return tt(
				"cdn.reason.challenge",
				"Cloudflare shows a challenge: add the domain to the security skip rule.",
			);
		case "ssl_flexible":
			return tt(
				"cdn.reason.sslFlexible",
				"SSL mode is Flexible: set it to Full.",
			);
		case "ssl_strict":
			return tt(
				"cdn.reason.sslStrict",
				"SSL mode is Full (strict): set it to Full.",
			);
		case "origin_down":
			return tt(
				"cdn.reason.originDown",
				"Cloudflare cannot reach the server: add the domain to the Origin Rule (port {{port}}).",
				{ port: status },
			);
		case "waf_block":
			return tt(
				"cdn.reason.waf",
				"A Cloudflare security rule blocks it: add the domain to the skip rule.",
			);
		case "unreachable":
			return tt(
				"cdn.reason.unreachable",
				"No answer through Cloudflare. Check the record and the Origin Rule.",
			);
		case "no_path":
			return tt(
				"cdn.reason.noPath",
				"No country config to test with; apply the countries first.",
			);
	}
	return tt("cdn.reason.other", "Unexpected answer (code {{code}}).", {
		code: status,
	});
};

const timeAgo = (value?: string): string => {
	if (!value) return "";
	const minutes = Math.max(
		0,
		Math.round((Date.now() - new Date(value).getTime()) / 60000),
	);
	if (minutes < 60) return `${minutes}m`;
	const hours = Math.round(minutes / 60);
	return hours < 48 ? `${hours}h` : `${Math.round(hours / 24)}d`;
};

const CopyValue: FC<{ label: string; value: string }> = ({ label, value }) => {
	const tt = useTT();
	const clip = useClipboard(value);
	return (
		<HStack
			justify="space-between"
			spacing={2}
			borderWidth="1px"
			borderRadius="6px"
			px={2}
			py={1}
		>
			<Box minW={0}>
				<Text fontSize="10px" opacity={0.7}>
					{label}
				</Text>
				<Text fontSize="xs" fontFamily="mono" dir="ltr" wordBreak="break-all">
					{value}
				</Text>
			</Box>
			<Button size="xs" variant="ghost" onClick={clip.onCopy} flexShrink={0}>
				{clip.hasCopied ? tt("tsin.copied", "Copied") : tt("cdn.copy", "Copy")}
			</Button>
		</HStack>
	);
};

const CDNDomainsDialog: FC<{
	isOpen: boolean;
	onClose: () => void;
	view?: CDNDomainsView;
}> = ({ isOpen, onClose, view }) => {
	const tt = useTT();
	const toast = useToast();
	const queryClient = useQueryClient();
	const [domain, setDomain] = useState("");
	const [note, setNote] = useState("");
	const [busy, setBusy] = useState<string>("");
	const [confirm, setConfirm] = useState<CDNDomain | null>(null);

	const act = async (key: string, body: Record<string, unknown>) => {
		if (busy) return;
		setBusy(key);
		try {
			const result = await fetch<CDNDomainsView>("/core/cdn-domains", {
				method: "POST",
				body,
			});
			queryClient.setQueryData([cdnDomainsQueryKey], result);
			if (body.action === "activate") {
				toast({
					status: "success",
					isClosable: true,
					duration: 12_000,
					title: tt(
						"cdn.activated",
						"Done. {{count}} configs now use the new domain; users get it when they update their subscription. Nothing restarted.",
						{ count: result.moved ?? 0 },
					),
				});
			} else if (result.check) {
				toast({
					status: result.check.state === "ok" ? "success" : "warning",
					isClosable: true,
					duration: 10_000,
					title:
						result.check.state === "ok"
							? tt("cdn.checkOk", "Works through Cloudflare.")
							: reasonText(tt, result.check.reason, result.check.status),
				});
			} else if (result.iran) {
				toast({
					status: result.iran.open === 0 ? "warning" : "success",
					isClosable: true,
					title: tt(
						"cdn.iranResult",
						"{{open}} of {{total}} test servers in Iran's datacenters got an answer.",
						result.iran,
					),
				});
			}
			if (body.action === "add") {
				setDomain("");
				setNote("");
			}
		} catch (error: any) {
			const check = error?.data?.check;
			toast({
				status: "error",
				isClosable: true,
				duration: 12_000,
				title: error?.data?.detail ?? String(error),
				description: check
					? reasonText(tt, check.reason, check.status)
					: undefined,
			});
		} finally {
			setBusy("");
		}
	};

	const domains = view?.domains ?? [];
	const ready = domains.filter(
		(item) => !item.active && item.check_state === "ok",
	).length;

	return (
		<>
			<AppDialog
				isOpen={isOpen}
				onClose={onClose}
				size="xl"
				title={tt("cdn.title", "Cloudflare domains")}
				footer={
					<Button variant="ghost" onClick={onClose}>
						{tt("close", "Close")}
					</Button>
				}
			>
				<Stack spacing={4}>
					<Text fontSize="sm">
						{tt(
							"cdn.intro",
							"Keep a few domains ready, on different Cloudflare accounts if you can. When the domain in use is filtered or its account is banned, press “Use this” on a ready one: only the configs' address changes, nobody is disconnected and nothing restarts. Users get it when they update their subscription. The panel checks every domain every half hour and the one in use from Iran every three hours, and tells you on Telegram.",
						)}
					</Text>
					{!view?.path_mode && (
						<Text fontSize="sm" color="orange.400">
							{tt(
								"cdn.noPathMode",
								"The countries are not served through Cloudflare (path mode) yet, so a domain cannot be put in use.",
							)}
						</Text>
					)}

					<Box borderWidth="1px" borderRadius="6px" p={3}>
						<Text fontWeight="semibold" mb={2}>
							{tt("cdn.list", "Domains")} ({domains.length}){" "}
							<Badge colorScheme={ready > 0 ? "green" : "orange"} ms={1}>
								{tt("cdn.readyCount", "{{count}} ready", { count: ready })}
							</Badge>
						</Text>
						<Stack spacing={2}>
							{domains.map((item) => {
								const ok = item.check_state === "ok";
								const checked = Boolean(item.checked_at);
								return (
									<Box
										key={item.id}
										borderWidth="1px"
										borderRadius="6px"
										p={2}
										borderColor={item.active ? "primary.400" : undefined}
									>
										<HStack justify="space-between" align="start" spacing={2}>
											<Box minW={0}>
												<HStack spacing={2} flexWrap="wrap">
													<Text
														fontFamily="mono"
														fontSize="sm"
														dir="ltr"
														wordBreak="break-all"
													>
														{item.domain}
													</Text>
													{item.active && (
														<Badge colorScheme="purple">
															{tt("cdn.inUse", "In use")}
														</Badge>
													)}
													<Badge
														colorScheme={
															!checked ? "gray" : ok ? "green" : "red"
														}
													>
														{!checked
															? tt("cdn.unchecked", "Not checked")
															: ok
																? tt("cdn.works", "Works")
																: tt("cdn.broken", "Not working")}
													</Badge>
													{item.iran_checked_at && !item.iran_error && (
														<Badge
															colorScheme={
																item.iran_open === 0
																	? "red"
																	: item.iran_open * 2 >= item.iran_total
																		? "green"
																		: "orange"
															}
														>
															{tt("cdn.iranBadge", "Iran {{open}}/{{total}}", {
																open: item.iran_open,
																total: item.iran_total,
															})}
														</Badge>
													)}
												</HStack>
												{item.note && (
													<Text fontSize="xs" opacity={0.75}>
														{item.note}
													</Text>
												)}
												{checked && !ok && (
													<Text fontSize="xs" color="red.400">
														{reasonText(
															tt,
															item.check_reason,
															item.check_status,
														)}
													</Text>
												)}
												{checked && (
													<Text fontSize="10px" opacity={0.6}>
														{tt("cdn.checkedAgo", "Checked {{ago}} ago", {
															ago: timeAgo(item.checked_at),
														})}
													</Text>
												)}
											</Box>
										</HStack>
										<HStack spacing={2} mt={2} flexWrap="wrap">
											<Button
												size="xs"
												variant="outline"
												isLoading={busy === `check-${item.id}`}
												onClick={() =>
													void act(`check-${item.id}`, {
														action: "check",
														id: item.id,
													})
												}
											>
												{tt("cdn.check", "Check")}
											</Button>
											<Button
												size="xs"
												variant="outline"
												isLoading={busy === `iran-${item.id}`}
												onClick={() =>
													void act(`iran-${item.id}`, {
														action: "iran",
														id: item.id,
													})
												}
											>
												{tt("cdn.checkIran", "Check from Iran")}
											</Button>
											{!item.active && (
												<>
													<Button
														size="xs"
														colorScheme="primary"
														isDisabled={!view?.path_mode || Boolean(busy)}
														onClick={() => setConfirm(item)}
													>
														{tt("cdn.use", "Use this")}
													</Button>
													<Button
														size="xs"
														variant="ghost"
														colorScheme="red"
														isLoading={busy === `remove-${item.id}`}
														onClick={() =>
															void act(`remove-${item.id}`, {
																action: "remove",
																id: item.id,
															})
														}
													>
														{tt("cdn.remove", "Remove")}
													</Button>
												</>
											)}
										</HStack>
									</Box>
								);
							})}
						</Stack>
					</Box>

					<Box borderWidth="1px" borderRadius="6px" p={3}>
						<Text fontWeight="semibold" mb={2}>
							{tt("cdn.addTitle", "Add a ready domain")}
						</Text>
						<SimpleGrid columns={{ base: 1, sm: 2 }} spacing={2}>
							<FormControl>
								<FormLabel fontSize="sm" mb={1}>
									{tt("cdn.domain", "Domain")}
								</FormLabel>
								<Input
									value={domain}
									onChange={(event) => setDomain(event.target.value)}
									placeholder="g2.example.com"
									dir="ltr"
									autoComplete="off"
									spellCheck={false}
								/>
							</FormControl>
							<FormControl>
								<FormLabel fontSize="sm" mb={1}>
									{tt("cdn.note", "Note (optional)")}
								</FormLabel>
								<Input
									value={note}
									onChange={(event) => setNote(event.target.value)}
									placeholder={tt(
										"cdn.notePlaceholder",
										"Cloudflare account 2",
									)}
								/>
							</FormControl>
						</SimpleGrid>
						<Button
							size="sm"
							mt={2}
							colorScheme="primary"
							isDisabled={!domain.trim()}
							isLoading={busy === "add"}
							onClick={() =>
								void act("add", { action: "add", domain: domain.trim(), note })
							}
						>
							{tt("cdn.add", "Add and check")}
						</Button>
					</Box>

					<Box borderWidth="1px" borderRadius="6px" p={3}>
						<Text fontWeight="semibold" mb={1}>
							{tt("cdn.setupTitle", "Setting a new domain up in Cloudflare")}
						</Text>
						<Stack spacing={1} fontSize="sm" mb={2}>
							<Text>
								{tt(
									"cdn.setup1",
									"1) Add the domain to a Cloudflare account (a second account spreads the risk of a ban).",
								)}
							</Text>
							<Text>
								{tt(
									"cdn.setup2",
									"2) DNS: an A record for the domain pointing to the value below, with the cloud orange (proxied).",
								)}
							</Text>
							<Text>
								{tt(
									"cdn.setup3",
									"3) SSL/TLS: set the mode to Full (not strict).",
								)}
							</Text>
							<Text>
								{tt(
									"cdn.setup4",
									"4) Rules → Origin Rules: the expression below, destination port {{port}}.",
									{ port: view?.origin_port ?? 2087 },
								)}
							</Text>
							<Text>
								{tt(
									"cdn.setup5",
									"5) Security → WAF: a skip rule with the same expression, so users are not challenged.",
								)}
							</Text>
							<Text>
								{tt(
									"cdn.setup6",
									"6) Add it here and press Check until it says Works. The expression already lists every domain here, so after adding one, copy it again into both rules.",
								)}
							</Text>
						</Stack>
						<Stack spacing={2}>
							<CopyValue
								label={tt("cdn.recordValue", "A record value")}
								value={
									view?.origin_ip || tt("cdn.thisServerIP", "this server's IP")
								}
							/>
							<CopyValue
								label={tt("cdn.originPort", "Origin Rule destination port")}
								value={String(view?.origin_port ?? 2087)}
							/>
							{view?.expression && (
								<CopyValue
									label={tt("cdn.expression", "Expression for both rules")}
									value={view.expression}
								/>
							)}
						</Stack>
					</Box>
				</Stack>
			</AppDialog>
			<ConfirmDialog
				isOpen={Boolean(confirm)}
				onClose={() => setConfirm(null)}
				isLoading={busy === "activate"}
				title={tt("cdn.confirmTitle", "Use {{domain}}?", {
					domain: confirm?.domain ?? "",
				})}
				description={tt(
					"cdn.confirmText",
					"Every country config moves from {{from}} to {{to}}. The panel checks the new domain through Cloudflare first and stops if it does not work. Users are not disconnected; they get the new domain when they update their subscription, and the old one keeps working for them until then if it still works.",
					{ from: view?.active ?? "", to: confirm?.domain ?? "" },
				)}
				confirmLabel={tt("cdn.use", "Use this")}
				onConfirm={() => {
					const item = confirm;
					setConfirm(null);
					if (item) void act("activate", { action: "activate", id: item.id });
				}}
			/>
		</>
	);
};

// CDNDomainsPanel is the dashboard tile for the Cloudflare domain pool and the
// window it opens. It shows only on panels that serve the countries through
// Cloudflare.
export const CDNDomainsPanel: FC = () => {
	const tt = useTT();
	const dialog = useDisclosure();
	const trackBg = useColorModeValue("blackAlpha.100", "whiteAlpha.100");
	const { data, isLoading } = useQuery(
		[cdnDomainsQueryKey],
		() => fetch<CDNDomainsView>("/core/cdn-domains"),
		{ refetchInterval: 120_000, refetchOnWindowFocus: false, retry: false },
	);
	if (isLoading || !data || (!data.path_mode && !data.domains?.length)) {
		return null;
	}
	const domains = data.domains ?? [];
	const active = domains.find((item) => item.active);
	const ready = domains.filter(
		(item) => !item.active && item.check_state === "ok",
	).length;
	const activeBroken = active?.checked_at && active.check_state !== "ok";
	const iranBlocked =
		active?.iran_checked_at &&
		!active.iran_error &&
		active.iran_total >= 3 &&
		active.iran_open === 0;
	const color =
		activeBroken || iranBlocked
			? "red.400"
			: ready === 0
				? "orange.400"
				: "green.400";
	const label =
		activeBroken || iranBlocked
			? tt("cdn.tile.problem", "Problem")
			: ready === 0
				? tt("cdn.tile.noSpare", "No spare")
				: tt("cdn.tile.ok", "Ready");

	return (
		<>
			<Box
				role="button"
				tabIndex={0}
				onClick={dialog.onOpen}
				onKeyDown={(event) => {
					if (event.key === "Enter" || event.key === " ") {
						event.preventDefault();
						dialog.onOpen();
					}
				}}
				h="full"
				minH="170px"
				display="flex"
				flexDirection="column"
				borderWidth="1px"
				borderColor="panel.border"
				borderRadius="xl"
				bg="panel.surface"
				p={4}
				cursor="pointer"
				transition="border-color 0.2s ease"
				_hover={{ borderColor: "primary.400" }}
				_focusVisible={{ outline: "2px solid", outlineColor: "primary.400" }}
				position="relative"
				overflow="hidden"
			>
				<Box
					position="absolute"
					insetInlineStart={0}
					top={0}
					bottom={0}
					w="3px"
					bg={color}
				/>
				<HStack justify="space-between" spacing={2}>
					<HStack spacing={1.5} minW={0}>
						<Box as={CloudIcon} w="16px" h="16px" color="primary.400" />
						<Text fontSize="sm" fontWeight="semibold" noOfLines={1}>
							{tt("cdn.title", "Cloudflare domains")}
						</Text>
					</HStack>
					<HStack
						spacing={1.5}
						px={2}
						py={0.5}
						borderRadius="full"
						bg={trackBg}
						flexShrink={0}
					>
						<Box w="8px" h="8px" borderRadius="full" bg={color} />
						<Text fontSize="xs" fontWeight="semibold" color={color}>
							{label}
						</Text>
					</HStack>
				</HStack>
				<Box flex="1" py={2.5}>
					<Text fontSize="10px" color="panel.textMuted">
						{tt("cdn.tile.inUse", "In use")}
					</Text>
					<Text
						fontSize="sm"
						fontWeight="semibold"
						fontFamily="mono"
						dir="ltr"
						noOfLines={1}
						textAlign="start"
					>
						{data.active || "—"}
					</Text>
					{(activeBroken || iranBlocked) && active && (
						<Text fontSize="xs" color="red.400" noOfLines={2} mt={1}>
							{activeBroken
								? reasonText(tt, active.check_reason, active.check_status)
								: tt(
										"cdn.tile.iranBlocked",
										"No answer from Iran: probably filtered. Use a ready domain.",
									)}
						</Text>
					)}
				</Box>
				<HStack
					justify="space-between"
					pt={2}
					borderTopWidth="1px"
					borderColor="panel.border"
				>
					<Box>
						<Text fontSize="10px" color="panel.textMuted">
							{tt("cdn.tile.spare", "Ready spares")}
						</Text>
						<Text fontSize="xs" fontWeight="semibold" color={color}>
							{ready}
						</Text>
					</Box>
					<Box textAlign="end">
						<Text fontSize="10px" color="panel.textMuted">
							{tt("cdn.tile.iran", "From Iran")}
						</Text>
						<Text fontSize="xs" fontWeight="semibold" dir="ltr">
							{active?.iran_checked_at && !active.iran_error
								? `${active.iran_open}/${active.iran_total}`
								: "—"}
						</Text>
					</Box>
				</HStack>
				{isLoading && <Spinner size="xs" />}
			</Box>
			<CDNDomainsDialog
				isOpen={dialog.isOpen}
				onClose={dialog.onClose}
				view={data}
			/>
		</>
	);
};
