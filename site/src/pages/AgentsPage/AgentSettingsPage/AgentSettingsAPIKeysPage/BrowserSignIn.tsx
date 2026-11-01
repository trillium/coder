import { cn } from "cn";
import { useFormik } from "formik";
import { useId, useRef, useState } from "react";
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
import { FormField } from "#/components/FormField/FormField";
import { Input } from "#/components/Input/Input";
import { Link } from "#/components/Link/Link";
import { Spinner } from "#/components/Spinner/Spinner";
import { toast } from "#/components/Toaster/toast";
import { getFormHelpers } from "#/utils/formUtils";
import { parseBrowserCallbackInput } from "./parseBrowserCallbackInput";
import {
	isFailedGrantStatus,
	SignInSection,
	SignInStatusLine,
	terminalStatusMessage,
} from "./SignInSection";
import { SignInStartPanel } from "./SignInStartPanel";

type BrowserSignInProps = {
	provider: UserChatProviderConfig;
};

type CallbackFormValues = {
	callback: string;
};

/**
 * Browser PKCE sign-in for one BYOK provider. The user pastes the localhost
 * callback here; the exchange persists the credential server side and returns
 * no key material.
 */
export const BrowserSignIn: React.FC<BrowserSignInProps> = ({ provider }) => {
	const queryClient = useQueryClient();
	const instructionsId = useId();
	const resetButtonRef = useRef<HTMLButtonElement>(null);
	const [grant, setGrant] = useState<AIBrowserGrantInitiateResponse | null>(
		null,
	);
	const [exchangeResult, setExchangeResult] =
		useState<AIBrowserGrantExchangeResponse | null>(null);
	const [pasteNote, setPasteNote] = useState<string | null>(null);
	const [hasEnded, setHasEnded] = useState(false);

	const initiateMutation = useMutation(initiateUserBrowserGrant(queryClient));
	const exchangeMutation = useMutation(exchangeUserBrowserGrant(queryClient));
	const cancelMutation = useMutation(cancelUserBrowserGrant());

	const providerName = provider.display_name || provider.provider;
	const status = exchangeResult?.status;
	const isTerminal = isFailedGrantStatus(status);

	const form = useFormik<CallbackFormValues>({
		initialValues: { callback: "" },
		validateOnChange: false,
		validateOnBlur: false,
		validate: (values) => {
			const parsed = parseBrowserCallbackInput(values.callback);
			return parsed.ok ? {} : { callback: parsed.error };
		},
		onSubmit: (values, helpers) => {
			const parsed = parseBrowserCallbackInput(values.callback);
			if (!grant || !parsed.ok) {
				return;
			}
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
							// handleStart clears the rest of the panel state.
							helpers.resetForm();
							setGrant(null);
							setHasEnded(true);
							return;
						}
						setExchangeResult(result);
						resetButtonRef.current?.focus();
					},
					onError: (error) => {
						toast.error(
							getErrorMessage(error, "Error completing browser sign-in."),
							{ description: getErrorDetail(error) },
						);
					},
				},
			);
		},
	});
	const getFieldHelpers = getFormHelpers(form);

	const resetPanel = () => {
		setGrant(null);
		setExchangeResult(null);
		setPasteNote(null);
		form.resetForm();
	};

	const endFlow = () => {
		resetPanel();
		setHasEnded(true);
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
						cancelMutation.mutate({
							providerConfigId: provider.provider_id,
							grantId: next.grant_id,
						});
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
		form.setFieldError("callback", undefined);
		void form.setFieldValue("callback", text, false);
	};

	const handleReset = () => {
		if (!grant || isTerminal) {
			endFlow();
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
				onSettled: endFlow,
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
				autoFocus={hasEnded}
				onStart={handleStart}
			/>
		);
	}

	const statusLine: React.ReactNode = isFailedGrantStatus(status) ? (
		terminalStatusMessage(
			status,
			exchangeResult?.reauth_message,
			"This sign-in expired. Start over for a fresh one.",
		)
	) : (
		<>
			<Spinner loading={exchangeMutation.isPending} size="sm" />
			Waiting for the callback paste. The sign-in stays open until it expires.
		</>
	);
	const callbackField = getFieldHelpers("callback");

	return (
		<SignInSection>
			<p id={instructionsId} className="m-0 text-sm text-content-secondary">
				Open{" "}
				<Link href={grant.authorize_url} target="_blank" rel="noreferrer">
					{providerName} sign-in
				</Link>{" "}
				in your browser and approve it. Approving leads to a page that fails to
				load (localhost:1455, connection refused). That failure is expected.
				Copy the full address-bar URL of that failed page and paste it below.
				Pasting just the code also works.
			</p>
			<form className="flex flex-col gap-3" onSubmit={form.handleSubmit}>
				<div className="flex flex-col gap-2">
					<FormField
						field={callbackField}
						label="Authorization callback"
						control={(controlProps) => (
							<Input
								{...controlProps}
								aria-describedby={cn(
									instructionsId,
									controlProps["aria-describedby"],
								)}
								name={callbackField.name}
								value={callbackField.value}
								onChange={(event) => {
									callbackField.onChange(event);
									form.setFieldError("callback", undefined);
								}}
								onBlur={callbackField.onBlur}
								type="text"
								placeholder="http://localhost:1455/auth/callback?code=..."
								disabled={exchangeMutation.isPending}
								className={cn(
									"h-8 font-mono",
									callbackField.error && "border-border-destructive",
								)}
								spellCheck={false}
								autoComplete="off"
								autoFocus
							/>
						)}
					/>
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
						ref={resetButtonRef}
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
			<SignInStatusLine>{statusLine}</SignInStatusLine>
		</SignInSection>
	);
};
