import {
	type AnnotationSubmission,
	type HighlightItem,
	type HostToAnnotatorMessage,
	parseAnnotatorToHostMessage,
} from "@coder/annotator/protocol";
import { type RefObject, useEffect, useRef, useState } from "react";

interface UseAnnotatorBridgeOptions {
	// Resolves the window hosting the overlay: the preview iframe's content
	// window, or a popout the dashboard opened. Re-read on every message so
	// a remounted iframe is picked up without rebinding.
	getTargetWindow: () => Window | null | undefined;
	// Changes whenever the target is replaced (iframe remount, popout
	// opened or closed) so listeners rebind and state resets.
	targetKey: number;
	// Origin the target is expected to load from. Messages from any other
	// origin or window are ignored.
	targetOrigin: string | undefined;
	// The iframe whose load events reset state, when the target is the
	// frame. Popouts have no observable load and rely on the timeout alone.
	frameRef?: RefObject<HTMLIFrameElement | null>;
	// Nothing is listened to until the user has asked for the overlay, so a
	// preview that was never annotated cannot talk to the dashboard.
	enabled: boolean;
	// How long to wait for the overlay before declaring it unavailable
	// (blocked by CSP, non-HTML page, and so on). Counted from the frame's
	// load event, or from binding for a popout.
	readyTimeoutMs?: number;
	onSubmit: (submission: AnnotationSubmission) => void;
}

interface AnnotatorBridge {
	ready: boolean;
	// The target finished loading without the overlay announcing itself.
	unavailable: boolean;
	picking: boolean;
	setPicking: (picking: boolean) => void;
	highlight: (items: HighlightItem[]) => void;
	clearHighlights: () => void;
}

/**
 * Talks to the annotation overlay the app proxy injects into a proxied
 * preview, whether it lives in the right panel's iframe or in a popout
 * window. The overlay is cross-origin and shares its window with the
 * previewed app, so every inbound message is validated and bounded before
 * it reaches the caller.
 */
export function useAnnotatorBridge({
	getTargetWindow,
	targetKey,
	targetOrigin,
	frameRef,
	enabled,
	readyTimeoutMs = 5000,
	onSubmit,
}: UseAnnotatorBridgeOptions): AnnotatorBridge {
	const [ready, setReady] = useState(false);
	const [unavailable, setUnavailable] = useState(false);
	const [picking, setPickingState] = useState(false);
	// Picking requested before the overlay finished loading; applied once
	// the ready message arrives.
	const pendingPickingRef = useRef<boolean | null>(null);
	const onSubmitRef = useRef(onSubmit);
	useEffect(() => {
		onSubmitRef.current = onSubmit;
	}, [onSubmit]);

	const post = (message: HostToAnnotatorMessage) => {
		const target = getTargetWindow();
		if (target && targetOrigin) {
			target.postMessage(message, targetOrigin);
		}
	};

	useEffect(() => {
		if (!enabled || !targetOrigin) {
			return;
		}
		let readyTimer: ReturnType<typeof setTimeout> | undefined;
		const reset = () => {
			setReady(false);
			setPickingState(false);
			clearTimeout(readyTimer);
			readyTimer = setTimeout(() => setUnavailable(true), readyTimeoutMs);
		};
		const handler = (event: MessageEvent) => {
			const target = getTargetWindow();
			if (event.origin !== targetOrigin || !target || event.source !== target) {
				return;
			}
			const message = parseAnnotatorToHostMessage(event.data);
			if (!message) {
				return;
			}
			switch (message.type) {
				case "coder-annotator:ready":
					clearTimeout(readyTimer);
					setReady(true);
					setUnavailable(false);
					if (pendingPickingRef.current !== null) {
						target.postMessage(
							{
								type: "coder-annotator:set-picking",
								picking: pendingPickingRef.current,
							} satisfies HostToAnnotatorMessage,
							targetOrigin,
						);
						pendingPickingRef.current = null;
					}
					break;
				case "coder-annotator:state":
					setPickingState(message.picking);
					break;
				case "coder-annotator:submit": {
					const { type: _type, ...submission } = message;
					onSubmitRef.current(submission);
					break;
				}
			}
		};
		window.addEventListener("message", handler);
		// The overlay announces itself after the frame's own load event, and
		// postMessage delivery is queued behind it, so resetting here never
		// races a fresh ready message. A popout has no load event we can
		// observe, so it starts its timeout immediately.
		const frame = frameRef?.current;
		if (frame) {
			frame.addEventListener("load", reset);
		} else {
			reset();
		}
		return () => {
			clearTimeout(readyTimer);
			window.removeEventListener("message", handler);
			frame?.removeEventListener("load", reset);
			setReady(false);
			setUnavailable(false);
			setPickingState(false);
		};
	}, [
		getTargetWindow,
		targetKey,
		targetOrigin,
		frameRef,
		enabled,
		readyTimeoutMs,
	]);

	return {
		ready,
		unavailable,
		picking,
		setPicking: (next) => {
			if (!ready) {
				pendingPickingRef.current = next;
				return;
			}
			post({ type: "coder-annotator:set-picking", picking: next });
		},
		highlight: (items) => post({ type: "coder-annotator:highlight", items }),
		clearHighlights: () => post({ type: "coder-annotator:clear-highlights" }),
	};
}
