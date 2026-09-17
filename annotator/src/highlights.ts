import { viewportBox } from "./geometry";
import { annotationIdAttribute, type HighlightItem } from "./protocol";

interface PlacedHighlight extends HighlightItem {
	node: HTMLDivElement;
	// Last rect the selector resolved to; used briefly after the element
	// disappears (for example mid re-render) so the shimmer does not flicker.
	lastRect?: DOMRect;
	missingSince?: number;
}

// How long a highlight keeps its last position after its selector stops
// matching before it is hidden.
const highlightGraceMs = 3000;
// Same outset as the hover highlight so the two line up.
const highlightInset = 4;

interface HighlightLayer {
	set(items: HighlightItem[]): void;
	destroy(): void;
}

/**
 * Draws shimmering boxes over elements the agent is currently changing.
 * Highlights resolve their selector on every frame rather than holding a
 * node, so they follow re-renders, HMR swaps, and resizes.
 */
export function createHighlightLayer(
	doc: Document,
	win: Window,
	container: HTMLElement,
): HighlightLayer {
	const placed: PlacedHighlight[] = [];
	let loop = 0;

	// The stamped attribute is authoritative. The selector only fills in
	// while the stamped node is missing (mid re-render, HMR swap) and the
	// preview is still on the annotated page, so navigating to a page
	// with a similar element does not light it up.
	const resolve = (item: HighlightItem): Element | null => {
		const stamped = doc.querySelector(
			`[${annotationIdAttribute}="${item.id}"]`,
		);
		if (stamped) {
			return stamped;
		}
		if (!samePage(item.url, win.location.href)) {
			return null;
		}
		try {
			return doc.querySelector(item.selector);
		} catch {
			return null;
		}
	};

	const position = () => {
		const now = performance.now();
		for (const item of placed) {
			const target = resolve(item);
			let rect = target?.getBoundingClientRect();
			if (rect && (rect.width > 0 || rect.height > 0)) {
				item.lastRect = rect;
				item.missingSince = undefined;
			} else if (!samePage(item.url, win.location.href)) {
				// Off the annotated page: nothing to hold a position for.
				rect = undefined;
			} else {
				item.missingSince ??= now;
				rect =
					now - item.missingSince < highlightGraceMs
						? item.lastRect
						: undefined;
			}
			if (!rect) {
				item.node.style.display = "none";
				continue;
			}
			const box = viewportBox(rect, win, highlightInset);
			item.node.style.display = "block";
			item.node.style.left = `${box.left}px`;
			item.node.style.top = `${box.top}px`;
			item.node.style.width = `${box.width}px`;
			item.node.style.height = `${box.height}px`;
			// The beam's rotating gradient must cover the box's corners at
			// any angle, so it is sized from the diagonal.
			item.node.style.setProperty(
				"--diagonal",
				`${Math.ceil(Math.hypot(box.width, box.height))}px`,
			);
		}
	};

	const stop = () => {
		if (loop !== 0) {
			win.cancelAnimationFrame(loop);
			loop = 0;
		}
	};

	const clear = () => {
		stop();
		for (const item of placed.splice(0)) {
			item.node.remove();
		}
	};

	// Highlights are the only consumer of the stamp, so it goes when they
	// are cleared rather than lingering in the page's DOM.
	const unstamp = () => {
		for (const node of doc.querySelectorAll(`[${annotationIdAttribute}]`)) {
			node.removeAttribute(annotationIdAttribute);
		}
	};

	const set = (items: HighlightItem[]) => {
		clear();
		if (items.length === 0) {
			unstamp();
		}
		for (const item of items) {
			const node = doc.createElement("div");
			node.className = "shimmer";
			node.style.display = "none";
			const beam = doc.createElement("div");
			beam.className = "beam";
			node.append(beam);
			container.append(node);
			placed.push({ ...item, node });
		}
		if (placed.length === 0) {
			return;
		}
		// Pending highlights must track layout changes the page makes on its
		// own (agent-driven HMR updates), so they run a frame loop while any
		// exist instead of piggybacking on scroll and resize events.
		const tick = () => {
			position();
			loop = win.requestAnimationFrame(tick);
		};
		loop = win.requestAnimationFrame(tick);
	};

	return {
		set,
		destroy: () => {
			clear();
			unstamp();
		},
	};
}

function samePage(a: string, b: string): boolean {
	try {
		const left = new URL(a);
		const right = new URL(b);
		return left.origin === right.origin && left.pathname === right.pathname;
	} catch {
		return false;
	}
}
