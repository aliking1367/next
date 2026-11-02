import { useEffect } from "react";

// A focused number field changes its value when the mouse wheel turns over
// it, so scrolling a long form could quietly edit a port or a limit. This
// drops focus on the first wheel tick instead; the page scrolls as normal.
export const useNumberWheelGuard = () => {
	useEffect(() => {
		const onWheel = (event: WheelEvent) => {
			const target = event.target;
			if (
				target instanceof HTMLInputElement &&
				target.type === "number" &&
				target === document.activeElement
			) {
				target.blur();
			}
		};
		document.addEventListener("wheel", onWheel, { passive: true });
		return () => document.removeEventListener("wheel", onWheel);
	}, []);
};
