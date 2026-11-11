import {
	Badge,
	Box,
	Button,
	HStack,
	Stack,
	Text,
	useClipboard,
	useToast,
} from "@chakra-ui/react";
import { QRCodeCanvas } from "qrcode.react";
import { type FC, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetch } from "service/http";

type TSinLabTest = {
	tag: string;
	name: string;
	about: string;
	port: number;
	link: string;
};

type TSinLabView = { address: string; tests: TSinLabTest[] | null };

const useTT = () => {
	const { t } = useTranslation();
	return (key: string, fallback: string, options?: Record<string, unknown>) =>
		t(key, { defaultValue: fallback, ...options }) as string;
};

const LabTest: FC<{ test: TSinLabTest; index: number }> = ({ test, index }) => {
	const tt = useTT();
	const clip = useClipboard(test.link);
	const [showQR, setShowQR] = useState(false);
	return (
		<Box borderWidth="1px" borderRadius="6px" p={2}>
			<HStack justify="space-between" align="start" spacing={2}>
				<Box minW={0}>
					<Text fontSize="sm" fontWeight="semibold">
						{index + 1}. {test.name}
					</Text>
					<Text fontSize="xs" opacity={0.8}>
						{test.about}
					</Text>
					<Text fontSize="10px" opacity={0.6} dir="ltr">
						{tt("tsin.lab.port", "Port")} {test.port}
					</Text>
				</Box>
				<Stack spacing={1} flexShrink={0}>
					<Button size="xs" variant="outline" onClick={clip.onCopy}>
						{clip.hasCopied
							? tt("tsin.copied", "Copied")
							: tt("cdn.copy", "Copy")}
					</Button>
					<Button size="xs" variant="ghost" onClick={() => setShowQR(!showQR)}>
						QR
					</Button>
				</Stack>
			</HStack>
			{showQR && (
				<Box mt={2} p={2} bg="white" borderRadius="6px" w="fit-content">
					<QRCodeCanvas value={test.link} size={200} />
				</Box>
			)}
		</Box>
	);
};

// TSinLabSection is the protocol lab: a few test configs, each hiding VLESS a
// different way, through the same address as the ⚡ configs, to find which
// one a network lets through. They reach no user; remove them after testing.
export const TSinLabSection: FC = () => {
	const tt = useTT();
	const toast = useToast();
	const [view, setView] = useState<TSinLabView | null>(null);
	const [busy, setBusy] = useState("");

	const call = async (action?: "create" | "remove") => {
		if (busy) return;
		setBusy(action ?? "load");
		try {
			const result = await fetch<TSinLabView>(
				"/core/tsin/lab",
				action ? { method: "POST", body: { action } } : undefined,
			);
			setView(result);
			if (action === "create") {
				toast({
					status: "success",
					isClosable: true,
					duration: 10_000,
					title: tt(
						"tsin.lab.created",
						"Test configs made. Wait a minute for the server to load them, then test each on the phone.",
					),
				});
			}
		} catch (error: any) {
			toast({
				status: "error",
				isClosable: true,
				title: error?.data?.detail ?? String(error),
			});
		} finally {
			setBusy("");
		}
	};

	const tests = view?.tests ?? [];
	return (
		<Box mt={3} borderWidth="1px" borderRadius="6px" p={3}>
			<HStack justify="space-between" mb={1}>
				<Text fontWeight="semibold" fontSize="sm">
					{tt("tsin.lab.title", "🧪 Protocol test")}
				</Text>
				{view && (
					<Badge colorScheme={tests.length ? "purple" : "gray"}>
						{tests.length}
					</Badge>
				)}
			</HStack>
			<Text fontSize="xs" mb={2} opacity={0.85}>
				{tt(
					"tsin.lab.help",
					"Makes 5 test configs, each hiding the connection a different way, through the same address as the ⚡ configs (the front server) and out through Best ping. No user gets them. Scan each on the phone, on the network that has trouble, and see which connects; then remove them. Making or removing them reconnects the server's users for a second.",
				)}
			</Text>
			<HStack spacing={2} flexWrap="wrap">
				{!view && (
					<Button
						size="sm"
						variant="outline"
						isLoading={busy === "load"}
						onClick={() => void call()}
					>
						{tt("tsin.lab.show", "Show")}
					</Button>
				)}
				{view && tests.length === 0 && (
					<Button
						size="sm"
						colorScheme="primary"
						isLoading={busy === "create"}
						onClick={() => void call("create")}
					>
						{tt("tsin.lab.create", "Make test configs")}
					</Button>
				)}
				{tests.length > 0 && (
					<Button
						size="sm"
						variant="ghost"
						colorScheme="red"
						isLoading={busy === "remove"}
						onClick={() => void call("remove")}
					>
						{tt("tsin.lab.remove", "Remove test configs")}
					</Button>
				)}
			</HStack>
			{tests.length > 0 && (
				<Stack spacing={2} mt={3}>
					<Text fontSize="xs" opacity={0.75} dir="ltr">
						{view?.address}
					</Text>
					{tests.map((test, index) => (
						<LabTest key={test.tag} test={test} index={index} />
					))}
				</Stack>
			)}
		</Box>
	);
};
