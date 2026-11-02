import {
	Button,
	HStack,
	Modal,
	ModalBody,
	ModalCloseButton,
	ModalContent,
	ModalFooter,
	ModalHeader,
	ModalOverlay,
	Text,
	Textarea,
	useClipboard,
} from "@chakra-ui/react";
import {
	ClipboardDocumentCheckIcon,
	ClipboardDocumentIcon,
	ShareIcon,
} from "@heroicons/react/24/outline";
import { type FC, useMemo } from "react";
import { useTranslation } from "react-i18next";

export type AdminLogin = { username: string; password: string };

// The panel address as the new admin will type it: this page's address up to
// the admins route, so a panel behind a path prefix keeps that prefix.
const panelAddress = () => {
	const { origin, pathname } = window.location;
	const index = pathname.lastIndexOf("/admins");
	return `${origin}${index >= 0 ? pathname.slice(0, index) : ""}/`;
};

// Shown once after an admin is created, so the login can be handed over in
// one message instead of being retyped.
export const AdminLoginShareModal: FC<{
	login: AdminLogin | null;
	onClose: () => void;
}> = ({ login, onClose }) => {
	const { t } = useTranslation();
	const message = useMemo(
		() =>
			login
				? [
						t("admins.loginShare.intro"),
						`🌐 ${panelAddress()}`,
						`👤 ${t("username")}: ${login.username}`,
						`🔑 ${t("password")}: ${login.password}`,
					].join("\n")
				: "",
		[login, t],
	);
	const { hasCopied, onCopy } = useClipboard(message);
	const canShare = typeof navigator !== "undefined" && "share" in navigator;

	return (
		<Modal isOpen={Boolean(login)} onClose={onClose} isCentered size="md">
			<ModalOverlay />
			<ModalContent mx={3}>
				<ModalHeader>{t("admins.loginShare.title")}</ModalHeader>
				<ModalCloseButton />
				<ModalBody>
					<Text fontSize="sm" color="gray.500" mb={3}>
						{t("admins.loginShare.hint")}
					</Text>
					<Textarea
						value={message}
						isReadOnly
						dir="auto"
						rows={5}
						fontFamily="mono"
						fontSize="sm"
						onFocus={(event) => event.currentTarget.select()}
					/>
				</ModalBody>
				<ModalFooter>
					<HStack spacing={2} w="full" justify="flex-end" flexWrap="wrap">
						{canShare && (
							<Button
								size="sm"
								variant="outline"
								leftIcon={<ShareIcon width={16} />}
								onClick={() => {
									navigator.share({ text: message }).catch(() => undefined);
								}}
							>
								{t("admins.loginShare.share")}
							</Button>
						)}
						<Button
							size="sm"
							colorScheme="primary"
							leftIcon={
								hasCopied ? (
									<ClipboardDocumentCheckIcon width={16} />
								) : (
									<ClipboardDocumentIcon width={16} />
								)
							}
							onClick={onCopy}
						>
							{hasCopied
								? t("admins.loginShare.copied")
								: t("admins.loginShare.copy")}
						</Button>
					</HStack>
				</ModalFooter>
			</ModalContent>
		</Modal>
	);
};
