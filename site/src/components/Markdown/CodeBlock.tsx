import { cn } from "cn";
import { CopyIcon } from "lucide-react";
import { type FC, type PropsWithChildren, useRef } from "react";
import { CheckIcon } from "#/components/AnimatedIcons/Check";
import { Button } from "#/components/Button/Button";
import { CopyButton } from "#/components/CopyButton/CopyButton";
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "#/components/Tooltip/Tooltip";
import { useClipboard } from "#/hooks/useClipboard";

/**
 * Overlay positioning shared by every copy button on a code block. The button
 * is absolutely positioned so it never shifts the block layout, and it stays
 * invisible at rest so blocks look exactly as they did before.
 */
const copyOverlayClassName = cn(
	"absolute right-2 top-2",
	"opacity-0 transition-opacity",
	"group-hover/codeblock:opacity-100 group-focus-within/codeblock:opacity-100",
	"[&_button]:bg-surface-secondary [&_button]:shadow-md",
);

type CodeBlockProps = PropsWithChildren<{
	/**
	 * The exact text copied to the clipboard when the button is clicked.
	 */
	code: string;
}>;

/**
 * Wraps a rendered pre code block with the existing copy iconography. The
 * button appears top-right on hover (or keyboard focus) and copies the exact
 * block text. UI layer only; no gateway or agent behavior changes.
 */
export const CodeBlock: FC<CodeBlockProps> = ({ code, children }) => {
	return (
		<div className="relative group/codeblock">
			{children}
			<CopyButton
				text={code}
				label="Copy code"
				tooltipSide="left"
				className={copyOverlayClassName}
			/>
		</div>
	);
};

type CopyablePreProps = PropsWithChildren<{
	label?: string;
}>;

/**
 * Fallback for pre blocks whose text cannot be determined statically (rich
 * element children instead of plain text). Reads the rendered text at click
 * time so the copied value is still the exact block text.
 */
export const CopyablePre: FC<CopyablePreProps> = ({
	label = "Copy code",
	children,
}) => {
	const containerRef = useRef<HTMLDivElement>(null);
	const { showCopiedSuccess, copyToClipboard } = useClipboard();

	return (
		<div ref={containerRef} className="relative group/codeblock">
			{children}
			<Tooltip>
				<TooltipTrigger asChild>
					<Button
						size="icon"
						variant="subtle"
						aria-label={label}
						className={copyOverlayClassName}
						onClick={() => {
							void copyToClipboard(
								containerRef.current?.querySelector("pre")?.textContent ?? "",
							);
						}}
					>
						{showCopiedSuccess ? <CheckIcon /> : <CopyIcon />}
					</Button>
				</TooltipTrigger>
				<TooltipContent side="left">{label}</TooltipContent>
			</Tooltip>
		</div>
	);
};
