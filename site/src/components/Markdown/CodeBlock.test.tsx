import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { TooltipProvider } from "#/components/Tooltip/Tooltip";
import { Markdown } from "./Markdown";

const codeSample = 'echo "hello world"';

function setupClipboardStub() {
	let copied: string | undefined;
	const writeText = async (text: string) => {
		copied = text;
	};
	Object.defineProperty(window.navigator, "clipboard", {
		value: { writeText },
		configurable: true,
	});
	return {
		copied: () => copied,
	};
}

describe("Markdown code blocks", () => {
	it("Reveals a copy button on hover that copies the exact block text", async () => {
		const clipboard = setupClipboardStub();

		render(
			<TooltipProvider>
				<Markdown>{`\`\`\`sh\n${codeSample}\n\`\`\``}</Markdown>
			</TooltipProvider>,
		);

		const copyButton = screen.getByRole("button", { name: "Copy code" });
		// No visual change at rest: the button starts invisible and only
		// appears on hover/focus via the group-hover opacity classes.
		expect(copyButton).toHaveClass("opacity-0");
		expect(copyButton.className).toContain("group-hover/codeblock:opacity-100");

		fireEvent.click(copyButton);
		await waitFor(() => {
			expect(clipboard.copied()).toBe(codeSample);
		});
	});

	it("Renders inline code without a copy button", () => {
		render(
			<TooltipProvider>
				<Markdown>Use `echo hi` inline</Markdown>
			</TooltipProvider>,
		);

		expect(
			screen.queryByRole("button", { name: "Copy code" }),
		).not.toBeInTheDocument();
	});
});
