import {
	Badge,
	Box,
	Button,
	ButtonGroup,
	HStack,
	Modal,
	ModalBody,
	ModalCloseButton,
	ModalContent,
	ModalFooter,
	ModalHeader,
	ModalOverlay,
	SimpleGrid,
	Skeleton,
	Text,
} from "@chakra-ui/react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "react-query";
import { fetch as apiFetch } from "service/http";

type TemplateList = { templates: string[] };
type TemplatePreview = { name: string; html: string };

// Pages are rendered at phone size, then scaled down for the thumbnails.
const PHONE_WIDTH = 390;
const DESKTOP_WIDTH = 1100;
const PAGE_HEIGHT = 780;
const THUMB_SCALE = 0.4;

// Inside the sandbox the page has no origin, so localStorage and
// document.cookie throw and some templates would stop running. Give them
// in-memory stand-ins instead (preview only).
const STORAGE_SHIM =
	"<script>(function(){try{window.localStorage.length}catch(e){var m={};Object.defineProperty(window,'localStorage',{configurable:true,value:{getItem:function(k){return k in m?m[k]:null},setItem:function(k,v){m[k]=String(v)},removeItem:function(k){delete m[k]},clear:function(){m={}},key:function(i){return Object.keys(m)[i]||null},get length(){return Object.keys(m).length}}})}try{document.cookie}catch(e){var c='';Object.defineProperty(document,'cookie',{configurable:true,get:function(){return c},set:function(v){c=String(v).split(';')[0]}})}})();</script>";

const withStorageShim = (html: string) =>
	/<head[^>]*>/i.test(html)
		? html.replace(/<head[^>]*>/i, (tag) => tag + STORAGE_SHIM)
		: STORAGE_SHIM + html;

const templateSlug = (name: string) =>
	name.replace(/^subscription\//, "").replace(/\.html$/, "");

const useTemplatePreview = (name: string | null) =>
	useQuery(
		["subscription-page-template-preview", name],
		() =>
			apiFetch<TemplatePreview>(
				"/settings/subscriptions/page-templates/preview",
				{ query: { name } },
			),
		{ enabled: Boolean(name), staleTime: 5 * 60 * 1000 },
	);

// The page runs in a sandbox without same-origin access, so template scripts
// can never reach the dashboard's session.
const PreviewFrame = ({
	html,
	width,
	scale = 1,
	interactive = false,
}: {
	html: string;
	width: number;
	scale?: number;
	interactive?: boolean;
}) => (
	<Box
		position="relative"
		w={`${width * scale}px`}
		h={`${PAGE_HEIGHT * scale}px`}
		maxW="100%"
		overflow="hidden"
		borderRadius="lg"
		borderWidth="1px"
		borderColor="panel.border"
		pointerEvents={interactive ? "auto" : "none"}
	>
		<iframe
			title="subscription page preview"
			srcDoc={withStorageShim(html)}
			sandbox="allow-scripts"
			loading="lazy"
			scrolling={interactive ? "auto" : "no"}
			tabIndex={interactive ? 0 : -1}
			style={{
				position: "absolute",
				top: 0,
				left: 0,
				width,
				height: PAGE_HEIGHT,
				border: 0,
				transform: scale === 1 ? undefined : `scale(${scale})`,
				transformOrigin: "top left",
			}}
		/>
	</Box>
);

const TemplateCard = ({
	name,
	label,
	selected,
	onUse,
	onPreview,
}: {
	name: string;
	label: string;
	selected: boolean;
	onUse: () => void;
	onPreview: () => void;
}) => {
	const { t } = useTranslation();
	const preview = useTemplatePreview(name);
	return (
		<Box
			borderWidth="2px"
			borderColor={selected ? "panel.accent" : "panel.border"}
			borderRadius="xl"
			p={3}
			bg="panel.surface"
		>
			<Box
				as="button"
				type="button"
				display="block"
				mx="auto"
				onClick={onPreview}
				aria-label={t("settings.subscriptions.templateGallery.preview")}
			>
				{preview.data ? (
					<PreviewFrame
						html={preview.data.html}
						width={PHONE_WIDTH}
						scale={THUMB_SCALE}
					/>
				) : (
					<Skeleton
						w={`${PHONE_WIDTH * THUMB_SCALE}px`}
						h={`${PAGE_HEIGHT * THUMB_SCALE}px`}
						borderRadius="lg"
					/>
				)}
			</Box>
			{preview.isError && (
				<Text mt={2} fontSize="xs" color="red.400">
					{t("settings.subscriptions.templateGallery.error")}
				</Text>
			)}
			<HStack justify="space-between" mt={3} spacing={2}>
				<Text fontWeight="semibold" fontSize="sm" noOfLines={1}>
					{label}
				</Text>
				{selected && (
					<Badge colorScheme="green" flexShrink={0}>
						{t("settings.subscriptions.templateGallery.inUse")}
					</Badge>
				)}
			</HStack>
			<HStack mt={2} spacing={2}>
				<Button
					size="sm"
					colorScheme="primary"
					flex="1"
					onClick={onUse}
					isDisabled={selected}
				>
					{t("settings.subscriptions.templateGallery.use")}
				</Button>
				<Button size="sm" variant="outline" onClick={onPreview}>
					{t("settings.subscriptions.templateGallery.preview")}
				</Button>
			</HStack>
		</Box>
	);
};

export const SubscriptionTemplateGallery = ({
	value,
	onSelect,
}: {
	value: string;
	onSelect: (name: string) => void;
}) => {
	const { t } = useTranslation();
	const [previewName, setPreviewName] = useState<string | null>(null);
	const [previewWidth, setPreviewWidth] = useState(PHONE_WIDTH);
	const list = useQuery(
		"subscription-page-templates",
		() => apiFetch<TemplateList>("/settings/subscriptions/page-templates"),
		{ staleTime: 5 * 60 * 1000 },
	);
	const fullPreview = useTemplatePreview(previewName);
	const current = (value || "").trim() || "subscription/index.html";
	const labelFor = (name: string) => {
		const key = `settings.subscriptions.templateGallery.names.${templateSlug(name)}`;
		const translated = t(key);
		return translated === key ? templateSlug(name) : translated;
	};

	if (list.isLoading) {
		return <Skeleton h="220px" borderRadius="xl" />;
	}
	const templates = list.data?.templates ?? [];
	if (!templates.length) {
		return (
			<Text fontSize="sm" color="panel.textMuted">
				{t("settings.subscriptions.templateGallery.empty")}
			</Text>
		);
	}

	return (
		<>
			<SimpleGrid minChildWidth="190px" spacing={3}>
				{templates.map((name) => (
					<TemplateCard
						key={name}
						name={name}
						label={labelFor(name)}
						selected={name === current}
						onUse={() => onSelect(name)}
						onPreview={() => {
							setPreviewWidth(PHONE_WIDTH);
							setPreviewName(name);
						}}
					/>
				))}
			</SimpleGrid>

			<Modal
				isOpen={Boolean(previewName)}
				onClose={() => setPreviewName(null)}
				size={previewWidth === PHONE_WIDTH ? "md" : "6xl"}
				scrollBehavior="inside"
				isCentered
			>
				<ModalOverlay />
				<ModalContent>
					<ModalHeader fontSize="md">
						{previewName ? labelFor(previewName) : ""}
					</ModalHeader>
					<ModalCloseButton />
					<ModalBody display="flex" flexDirection="column" alignItems="center" gap={3}>
						<ButtonGroup size="xs" isAttached variant="outline">
							<Button
								isActive={previewWidth === PHONE_WIDTH}
								onClick={() => setPreviewWidth(PHONE_WIDTH)}
							>
								{t("settings.subscriptions.templateGallery.phone")}
							</Button>
							<Button
								isActive={previewWidth === DESKTOP_WIDTH}
								onClick={() => setPreviewWidth(DESKTOP_WIDTH)}
							>
								{t("settings.subscriptions.templateGallery.desktop")}
							</Button>
						</ButtonGroup>
						{fullPreview.data ? (
							<PreviewFrame
								html={fullPreview.data.html}
								width={previewWidth}
								interactive
							/>
						) : (
							<Skeleton w={`${PHONE_WIDTH}px`} maxW="100%" h={`${PAGE_HEIGHT}px`} borderRadius="lg" />
						)}
						<Text fontSize="xs" color="panel.textMuted" textAlign="center">
							{t("settings.subscriptions.templateGallery.demoNote")}
						</Text>
					</ModalBody>
					<ModalFooter gap={2}>
						<Button variant="ghost" onClick={() => setPreviewName(null)}>
							{t("close")}
						</Button>
						<Button
							colorScheme="primary"
							isDisabled={!previewName || previewName === current}
							onClick={() => {
								if (previewName) onSelect(previewName);
								setPreviewName(null);
							}}
						>
							{t("settings.subscriptions.templateGallery.use")}
						</Button>
					</ModalFooter>
				</ModalContent>
			</Modal>
		</>
	);
};

export default SubscriptionTemplateGallery;
