import { useEffect } from "react";

// On phones every dialog opens as a bottom sheet (see index.scss). This lets
// the sheet be dragged down from its handle or header and closes it past a
// threshold, the way native sheets behave. It listens once on the document,
// so no dialog needs to opt in.
const PHONE_MAX_WIDTH = 768;
const HANDLE_ZONE = 32;
const CLOSE_DISTANCE = 110;

const closeSheet = (sheet: HTMLElement) => {
	const closeButton = sheet.querySelector<HTMLButtonElement>(
		".chakra-modal__close-btn",
	);
	if (closeButton) {
		closeButton.click();
		return;
	}
	sheet.dispatchEvent(
		new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
	);
};

export const useSheetDrag = () => {
	useEffect(() => {
		let sheet: HTMLElement | null = null;
		let startY = 0;
		let offset = 0;

		const onStart = (event: TouchEvent) => {
			if (window.innerWidth > PHONE_MAX_WIDTH || event.touches.length !== 1)
				return;
			const target = event.target as HTMLElement | null;
			const content = target?.closest<HTMLElement>(".chakra-modal__content");
			if (!content || content.classList.contains("rb-confirm-dialog-content"))
				return;
			// Only the handle strip and the header start a drag, so scrolling
			// the dialog body and using its inputs work as before.
			const touchY = event.touches[0].clientY;
			const inHandle =
				touchY - content.getBoundingClientRect().top <= HANDLE_ZONE;
			const inHeader = Boolean(target?.closest(".chakra-modal__header"));
			if (!inHandle && !inHeader) return;
			if (target?.closest("button, a, input, select, textarea")) return;
			sheet = content;
			startY = touchY;
			offset = 0;
		};

		const onMove = (event: TouchEvent) => {
			if (!sheet) return;
			offset = Math.max(0, event.touches[0].clientY - startY);
			sheet.classList.add("rb-sheet-dragging");
			sheet.classList.remove("rb-sheet-settling");
			sheet.style.transform = `translateY(${offset}px)`;
		};

		const onEnd = () => {
			if (!sheet) return;
			const current = sheet;
			sheet = null;
			current.classList.remove("rb-sheet-dragging");
			if (offset > CLOSE_DISTANCE) {
				closeSheet(current);
				return;
			}
			current.classList.add("rb-sheet-settling");
			current.style.transform = "";
			window.setTimeout(
				() => current.classList.remove("rb-sheet-settling"),
				240,
			);
		};

		document.addEventListener("touchstart", onStart, { passive: true });
		document.addEventListener("touchmove", onMove, { passive: true });
		document.addEventListener("touchend", onEnd);
		document.addEventListener("touchcancel", onEnd);
		return () => {
			document.removeEventListener("touchstart", onStart);
			document.removeEventListener("touchmove", onMove);
			document.removeEventListener("touchend", onEnd);
			document.removeEventListener("touchcancel", onEnd);
		};
	}, []);
};
