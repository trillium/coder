import { useId, useState } from "react";
import { useMutation, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	cancelUserBrowserGrant,
	exchangeUserBrowserGrant,
	initiateUserBrowserGrant,
} from "#/api/queries/chats";
import type {
	AIBrowserGrantExchangeResponse,
	AIBrowserGrantInitiateResponse,
	UserChatProviderConfig,
} from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import { Input } from "#/components/Input/Input";
import { Label } from "#/components/Label/Label";
import { Link } from "#/components/Link/Link";
import { Spinner } from "#/components/Spinner/Spinner";
import { toast } from "#/components/Toaster/toast";
import { parseBrowserCallbackInput } from "./parseBrowserCallbackInput";
import { SignInStartPanel } from "./SignInStartPanel";

type BrowserSignInProps = {
	provider: UserChatProviderConfig;
};

/**
 * Browser PKCE sign-in for one BYOK provider. The server builds the
 * authorize URL; the user approves there, then pastes the localhost callback
 * back here. The server checks the state and persists the credential, so the
 * exchange returns no key material.
 */
export const BrowserSignIn: React.FC<BrowserSignInProps> = ({ provider }) => {
	const queryClient = useQueryClient();
	const inputId = useId();
	const [grant, setGrant] = useState<AIBrowserGrantInitiateResponse | null>(
		null,
	);
	const [callbackInput, setCallbackInput] = useState("");
	const [exchangeResult, setExchangeResult] =
		useState<AIBrowserGrantExchangeResponse | null>(null);
	const [inputError, setInputError] = useState<string | null>(null);
	const [pasteNote, setPasteNote] = useState<string | null>(null);

	const initiateMutation = useMutation(initiateUserBrowserGrant(queryClient));
	const exchangeMutation = useMutation(exchangeUserBrowserGrant(queryClient));
	const cancelMutation = useMutation(cancelUserBrowserGrant());

	const providerName = provider.display_name || provider.provider;
	const status = exchangeResult?.status;
	const isTerminal =
		status === "expired" || status === "denied" || status === "canceled";

	const resetPanel = () => {
		setGrant(null);
		setCallbackInput("");
		setExchangeResult(null);
		setInputError(null);
		setPasteNote(null);
	};

	const handleStart = () => {
		initiateMutation.mutate(
			{ providerConfigId: provider.provider_id },
			{
				onSuccess: (next) => {
					if (!next.authorize_url) {
						toast.error(
							"Browser sign-in failed: no authorize URL received from the server.",
						);
						return;
					}
					resetPanel();
					setGrant(next);
					window.open(next.authorize_url, "_blank", "noopener,noreferrer");
				},
				onError: (error) => {
					toast.error(
						getErrorMessage(error, "Error starting browser sign-in."),
						{ description: getErrorDetail(error) },
					);
				},
			},
		);
	};

	const handleSubmit = (event: React.FormEvent) => {
		event.preventDefault();
		if (!grant || exchangeMutation.isPending) {
			return;
		}
		const parsed = parseBrowserCallbackInput(callbackInput);
		if (!parsed.ok) {
			setInputError(parsed.error);
			return;
		}
		setInputError(null);
		exchangeMutation.mutate(
			{
				providerConfigId: provider.provider_id,
				grantId: grant.grant_id,
				req: { input: parsed.input },
			},
			{
				onSuccess: (result) => {
					if (result.status === "authorized") {
						toast.success("Signed in. Personal key saved.");
						resetPanel();
						return;
					}
					setExchangeResult(result);
				},
				onError: (error) => {
					toast.error(
						getErrorMessage(error, "Error completing browser sign-in."),
						{ description: getErrorDetail(error) },
					);
				},
			},
		);
	};

	const handlePasteFromClipboard = async () => {
		if (!navigator.clipboard?.readText) {
			setPasteNote(
				"Clipboard read is not available in this browser. Copy the failed page's address and paste it manually.",
			);
			return;
		}
		let text: string;
		try {
			text = await navigator.clipboard.readText();
		} catch {
			setPasteNote(
				"Could not read the clipboard. Copy the failed page's address and paste it manually.",
			);
			return;
		}
		if (!text.trim()) {
			setPasteNote(
				"The clipboard was empty. Copy the failed page's address, then paste it manually.",
			);
			return;
		}
		setPasteNote(null);
		setInputError(null);
		setCallbackInput(text);
	};

	const handleReset = () => {
		if (!grant || isTerminal) {
			resetPanel();
			return;
		}
		cancelMutation.mutate(
			{ providerConfigId: provider.provider_id, grantId: grant.grant_id },
			{
				onError: (error) => {
					toast.error(getErrorMessage(error, "Error canceling sign-in."), {
						description: getErrorDetail(error),
					});
				},
				onSettled: resetPanel,
			},
		);
	};

	if (!grant) {
		return (
			<SignInStartPanel
				label={`${provider.reauth_required ? "Sign in again" : "Sign in"} with ${providerName} in your browser`}
				badge="Advanced alternative"
				badgeVariant="default"
				description="Opens the provider page in your browser. Approving leads to a page that fails to load (localhost:1455, connection refused). That failure is expected. Copy the full address-bar URL of that failed page and paste it back here."
				isPending={initiateMutation.isPending}
				onStart={handleStart}
			/>
		);
	}

	let statusLine: React.ReactNode;
	if (status === "expired") {
		statusLine =
			exchangeResult?.reauth_message ||
			"This sign-in expired. Start over for a fresh one.";
	} else if (status === "denied") {
		statusLine = "Sign-in was denied at the provider. Start over to try again.";
	} else if (status === "canceled") {
		statusLine = "Sign-in was canceled. Start over to try again.";
	} else {
		statusLine = (
			<>
				<Spinner loading={exchangeMutation.isPending} size="sm" />
				Waiting for the callback paste. The sign-in stays open until it expires.
			</>
		);
	}

	return (
		<div className="mt-6 flex flex-col gap-3 border-t border-solid border-border pt-6">
			<p className="m-0 text-sm text-content-secondary">
				Open{" "}
				<Link href={grant.authorize_url} target="_blank" rel="noreferrer">
					{providerName} sign-in
				</Link>{" "}
				in your browser and approve it. Approving leads to a page that fails to
				load (localhost:1455, connection refused). That failure is expected.
				Copy the full address-bar URL of that failed page and paste it below.
				Pasting just the code also works.
			</p>
			<form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
				<div className="flex flex-col gap-2">
					<Label htmlFor={inputId}>Authorization callback</Label>
					<Input
						id={inputId}
						type="text"
						value={callbackInput}
						onChange={(event) => {
							setCallbackInput(event.target.value);
							setInputError(null);
						}}
						placeholder="http://localhost:1455/auth/callback?code=..."
						disabled={exchangeMutation.isPending}
						className="h-8 font-mono"
						spellCheck={false}
						autoComplete="off"
						autoFocus
						aria-invalid={inputError !== null}
						aria-describedby={inputError ? `${inputId}-error` : undefined}
					/>
					{inputError && (
						<p
							id={`${inputId}-error`}
							role="alert"
							className="m-0 text-sm text-content-destructive"
						>
							{inputError}
						</p>
					)}
					{pasteNote && (
						<p role="status" className="m-0 text-sm text-content-secondary">
							{pasteNote}
						</p>
					)}
				</div>
				<div className="flex flex-wrap items-center gap-2">
					{!isTerminal && (
						<>
							<Button
								type="submit"
								size="sm"
								disabled={exchangeMutation.isPending}
							>
								<Spinner loading={exchangeMutation.isPending} />
								Complete sign-in
							</Button>
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={handlePasteFromClipboard}
								disabled={exchangeMutation.isPending}
							>
								Paste from clipboard
							</Button>
						</>
					)}
					<Button
						type="button"
						variant="outline"
						size="sm"
						onClick={handleReset}
						disabled={
							!isTerminal &&
							(cancelMutation.isPending || exchangeMutation.isPending)
						}
					>
						<Spinner loading={!isTerminal && cancelMutation.isPending} />
						{isTerminal ? "Start over" : "Cancel sign-in"}
					</Button>
				</div>
			</form>
			<p
				role="status"
				className="m-0 flex items-center gap-2 text-sm text-content-secondary"
			>
				{statusLine}
			</p>
		</div>
	);
};
