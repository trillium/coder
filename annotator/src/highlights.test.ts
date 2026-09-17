import { describe, expect, it } from "vitest";
import { createHighlightLayer } from "./highlights";
import { annotationIdAttribute } from "./protocol";

const nextFrame = () =>
	new Promise<void>((resolve) => {
		requestAnimationFrame(() => resolve());
	});

function setup(html: string) {
	document.body.innerHTML = html;
	const container = document.createElement("div");
	document.body.append(container);
	const layer = createHighlightLayer(document, window, container);
	return { layer, container };
}

describe("highlight layer", () => {
	it("follows the stamped element rather than the selector", async () => {
		const { layer, container } = setup(
			`<h1 id="other">Other</h1><h1 ${annotationIdAttribute}="a">Mine</h1>`,
		);
		const mine = document.querySelector(`[${annotationIdAttribute}]`);
		if (!(mine instanceof HTMLElement)) {
			throw new Error("missing stamped element");
		}
		mine.getBoundingClientRect = () => new DOMRect(100, 200, 50, 20);
		layer.set([{ id: "a", selector: "h1", url: window.location.href }]);
		await nextFrame();
		const box = container.firstElementChild as HTMLElement;
		expect(box.style.display).toBe("block");
		expect(box.style.left).toBe("96px");
		layer.destroy();
		expect(mine.hasAttribute(annotationIdAttribute)).toBe(false);
	});

	it("does not fall back to the selector on another page", async () => {
		const { layer, container } = setup("<h1>Same shape</h1>");
		const h1 = document.querySelector("h1") as HTMLElement;
		h1.getBoundingClientRect = () => new DOMRect(0, 0, 50, 20);
		layer.set([
			{ id: "a", selector: "h1", url: "http://localhost/some/other/page" },
		]);
		await nextFrame();
		const box = container.firstElementChild as HTMLElement;
		expect(box.style.display).toBe("none");
		layer.destroy();
	});
});
