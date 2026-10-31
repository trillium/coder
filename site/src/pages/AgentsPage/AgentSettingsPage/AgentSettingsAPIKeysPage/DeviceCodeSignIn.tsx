import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { getErrorDetail, getErrorMessage } from "#/api/errors";
import {
	cancelUserDeviceGrant,
	initiateUserDeviceGrant,
	invalidateChatProviderDependentQueries,
	isFinalDeviceGrantPollError,
	upsertUserChatProviderKey,
	userAIDeviceGrant,
} from "#/api/queries/chats";
import type {
	AIDeviceGrantInitiateResponse,
	UserChatProviderConfig,
} from "#/api/typesGenerated";
import { Button } from "#/components/Button/Button";
import { Link } from "#/components/Link/Link";
import { Spinner } from "#/components/Spinner/Spinner";
import { toast } from "#/components/Toaster/toast";
import {
	isFailedGrantStatus,
	SignInSection,
	SignInStatusLine,
	terminalStatusMessage,
} from "./SignInSection";
import { SignInStartPanel } from "./SignInStartPanel";

type DeviceCodeSignInProps = {
	provider: UserChatProviderConfig;
};

/**
 * Device-code sign-in for one BYOK provider. The user approves the code at
 * the provider, this panel polls the grant, and the approved credential is
 * saved as the user's personal key. The token itself is never shown.
 */
export const DeviceCodeSignIn: React.FC<DeviceCodeSignInProps> = ({
	provider,
}) => {
	const queryClient = useQueryClient();
	const [grant, setGrant] = useState<AIDeviceGrantInitiateResponse | null>(
		null,
	);
	const [handledGrantId, setHandledGrantId] = useState<string | null>(null);
	const [hasEnded, setHasEnded] = useState(false);

	const initiateMutation = useMutation(initiateUserDeviceGrant(queryClient));
	const cancelMutation = useMutation(cancelUserDeviceGrant());
	const {
		mutate: saveKey,
		reset: resetSave,
		isError: isSaveError,
	} = useMutation(upsertUserChatProviderKey(queryClient));

	const pollQuery = useQuery({
		...userAIDeviceGrant(
			provider.provider_id,
			grant?.grant_id ?? "",
			Math.max((grant?.poll_interval ?? 5) * 1000, 1000),
		),
		enabled: grant !== null,
		retry: false,
	});

	const status = pollQuery.data?.status;
	const providerName = provider.display_name || provider.provider;

	// Handle each authorized grant once, even if the save fails. Grants
	// without an api_key in the poll response were already persisted server
	// side, so only the dependent queries need a refresh.
	useEffect(() => {
		if (
			!grant ||
			status !== "authorized" ||
			handledGrantId === grant.grant_id
		) {
			return;
		}
		setHandledGrantId(grant.grant_id);
		const onSaved = () => {
			toast.success("Signed in. Personal key saved.");
			setGrant(null);
			setHasEnded(true);
		};
		const apiKey = pollQuery.data?.api_key;
		if (!apiKey) {
			void invalidateChatProviderDependentQueries(queryClient).then(onSaved);
			return;
		}
		saveKey(
			{ providerConfigId: provider.provider_id, req: { api_key: apiKey } },
			{
				onSuccess: onSaved,
				onError: (error) => {
					toast.error(getErrorMessage(error, "Error saving signed-in key."), {
						description: getErrorDetail(error),
					});
				},
			},
		);
	}, [
		grant,
		pollQuery.data,
		status,
		handledGrantId,
		provider.provider_id,
		queryClient,
		saveKey,
	]);

	const handleStart = () => {
		initiateMutation.mutate(
			{ providerConfigId: provider.provider_id },
			{
				onSuccess: (next) => {
					resetSave();
					setGrant(next);
				},
				onError: (error) => {
					toast.error(getErrorMessage(error, "Error starting sign-in."), {
						description: getErrorDetail(error),
					});
				},
			},
		);
	};

	const saveFailed = status === "authorized" && isSaveError;
	const pollGone =
		pollQuery.isError && isFinalDeviceGrantPollError(pollQuery.error);
	const isTerminal = saveFailed || pollGone || isFailedGrantStatus(status);

	const endFlow = () => {
		setGrant(null);
		setHasEnded(true);
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
				label={`${provider.reauth_required ? "Sign in again" : "Sign in"} with ${providerName}`}
				badge="Recommended"
				badgeVariant="green"
				description={
					provider.reauth_required
						? "Sign-in completes here, with no copy-paste step. Automatic refresh stopped, so approve a fresh code to continue."
						: `Sign-in completes here, with no copy-paste step. Approve the code at ${providerName} and the provider credential is saved as your personal key.`
				}
				isPending={initiateMutation.isPending}
				autoFocus={hasEnded}
				onStart={handleStart}
			/>
		);
	}

	let statusLine: React.ReactNode;
	if (pollGone) {
		statusLine = `${getErrorMessage(pollQuery.error, "This sign-in is no longer available.")} Start over to try again.`;
	} else if (pollQuery.isError) {
		statusLine = `${getErrorMessage(pollQuery.error, "Could not reach the sign-in check.")} Keep this open and approve the code; the check retries automatically.`;
	} else if (saveFailed) {
		statusLine =
			"Approved, but saving the key failed. Start over to try again.";
	} else if (isFailedGrantStatus(status)) {
		statusLine = terminalStatusMessage(
			status,
			pollQuery.data?.reauth_message,
			"This code expired. Start a fresh sign-in for a new code.",
		);
	} else if (status === "authorized") {
		statusLine = (
			<>
				<Spinner loading size="sm" />
				Approved. Saving your personal key.
			</>
		);
	} else {
		statusLine = (
			<>
				<Spinner loading size="sm" />
				Waiting for approval. This check repeats every{" "}
				{pollQuery.data?.poll_interval ?? grant.poll_interval} seconds until the
				code expires.
			</>
		);
	}

	return (
		<SignInSection>
			<p className="m-0 text-sm text-content-secondary">
				Open{" "}
				<Link
					href={grant.verification_uri}
					target="_blank"
					rel="noreferrer"
					autoFocus
				>
					{grant.verification_uri}
				</Link>{" "}
				and enter this code:
			</p>
			<p className="m-0 font-mono text-2xl font-semibold tracking-widest text-content-primary">
				{grant.user_code}
			</p>
			<SignInStatusLine>{statusLine}</SignInStatusLine>
			<div className="flex items-center gap-2">
				<Button
					type="button"
					variant="outline"
					size="sm"
					onClick={handleReset}
					disabled={
						!isTerminal && (cancelMutation.isPending || status === "authorized")
					}
				>
					<Spinner loading={!isTerminal && cancelMutation.isPending} />
					{isTerminal ? "Start over" : "Cancel sign-in"}
				</Button>
			</div>
		</SignInSection>
	);
};
