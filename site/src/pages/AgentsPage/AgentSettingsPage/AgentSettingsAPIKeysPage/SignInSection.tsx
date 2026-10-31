import { cn } from "cn";
import { isTerminalDeviceGrantStatus } from "#/api/queries/chats";
import type { AIDeviceGrantStatus } from "#/api/typesGenerated";

type FailedGrantStatus = Exclude<AIDeviceGrantStatus, "pending" | "authorized">;

/** Bordered section that separates each sign-in flow inside a provider panel. */
export const SignInSection: React.FC<React.ComponentProps<"div">> = ({
	className,
	...props
}) => (
	<div
		className={cn(
			"mt-6 flex flex-col gap-3 border-t border-solid border-border pt-6",
			className,
		)}
		{...props}
	/>
);

/** Live status line for an in-progress sign-in. */
export const SignInStatusLine: React.FC<{ children: React.ReactNode }> = ({
	children,
}) => (
	<p
		role="status"
		className="m-0 flex items-center gap-2 text-sm text-content-secondary"
	>
		{children}
	</p>
);

/** Reports whether a grant ended without authorizing. */
export const isFailedGrantStatus = (
	status: AIDeviceGrantStatus | undefined,
): status is FailedGrantStatus =>
	status !== undefined &&
	status !== "authorized" &&
	isTerminalDeviceGrantStatus(status);

/**
 * Message for a grant that ended without authorizing. An expired grant
 * prefers the server's re-auth message when it sent one.
 */
export const terminalStatusMessage = (
	status: FailedGrantStatus,
	reauthMessage: string | undefined,
	expiredFallback: string,
): string => {
	switch (status) {
		case "expired":
			return reauthMessage || expiredFallback;
		case "denied":
			return "Sign-in was denied at the provider. Start over to try again.";
		case "canceled":
			return "Sign-in was canceled. Start over to try again.";
	}
};
