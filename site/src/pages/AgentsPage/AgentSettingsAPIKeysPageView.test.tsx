import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { QueryClientProvider } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import type {
	AIDeviceGrantInitiateResponse,
	AIDeviceGrantPollResponse,
	UserAIProviderKeyConfig,
	UserChatProviderConfig,
} from "#/api/typesGenerated";
import { createTestQueryClient } from "#/testHelpers/renderHelpers";
import {
	AgentSettingsAPIKeysPageView,
	type AgentSettingsAPIKeysPageViewProps,
} from "./AgentSettingsAPIKeysPageView";

const createProvider = (
	overrides: Partial<UserChatProviderConfig> &
		Pick<UserChatProviderConfig, "provider_id" | "provider">,
): UserChatProviderConfig => ({
	provider_id: overrides.provider_id,
	provider: overrides.provider,
	display_name: overrides.display_name ?? overrides.provider,
	icon: overrides.icon ?? "",
	enabled: overrides.enabled ?? true,
	has_user_api_key: overrides.has_user_api_key ?? false,
	has_central_api_key_fallback: overrides.has_central_api_key_fallback ?? false,
	byok_enabled: overrides.byok_enabled ?? true,
	device_flow_supported: overrides.device_flow_supported ?? false,
	oauth_expiry: overrides.oauth_expiry,
	refresh_supported: overrides.refresh_supported ?? false,
	reauth_required: overrides.reauth_required ?? false,
});

const baseProvider = createProvider({
	provider_id: "prov-1",
	provider: "openai",
	display_name: "OpenAI",
});

const savedKeyConfig: UserAIProviderKeyConfig = {
	provider: {
		id: "prov-1",
		type: "openai",
		name: "openai",
		display_name: "OpenAI",
		icon: "",
		enabled: true,
		deleted: false,
	},
	has_user_api_key: true,
	has_provider_api_key: false,
	byok_enabled: true,
	device_flow_supported: false,
	refresh_supported: false,
	reauth_required: false,
};

const defaultProps: AgentSettingsAPIKeysPageViewProps = {
	error: undefined,
	isLoading: false,
	providers: [baseProvider],
	models: [],
	isModelsLoading: false,
	areModelsUnavailable: false,
};

const renderView = (ui: ReactElement) => {
	const queryClient = createTestQueryClient();
	queryClient.setDefaultOptions({
		...queryClient.getDefaultOptions(),
		mutations: { retry: false },
	});
	return render(
		<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>,
	);
};

afterEach(() => {
	vi.restoreAllMocks();
});

describe("AgentSettingsAPIKeysPageView", () => {
	it("saves a replacement key after a successful save remasks the field", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "upsertUserAIProviderKey").mockResolvedValue(
			savedKeyConfig,
		);

		renderView(<AgentSettingsAPIKeysPageView {...defaultProps} />);

		const apiKeyInput = screen.getByLabelText("API Key");
		await user.type(apiKeyInput, "sk-test-key");
		await user.click(screen.getByRole("button", { name: "Save" }));

		await waitFor(() => {
			expect(API.experimental.upsertUserAIProviderKey).toHaveBeenCalledWith(
				"prov-1",
				{ api_key: "sk-test-key" },
			);
		});

		await user.type(apiKeyInput, "sk-other-key");
		await user.click(screen.getByRole("button", { name: "Save" }));

		await waitFor(() => {
			expect(API.experimental.upsertUserAIProviderKey).toHaveBeenCalledTimes(2);
		});
		expect(API.experimental.upsertUserAIProviderKey).toHaveBeenLastCalledWith(
			"prov-1",
			{ api_key: "sk-other-key" },
		);
	});

	it("trims leading and trailing whitespace before saving", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "upsertUserAIProviderKey").mockResolvedValue(
			savedKeyConfig,
		);

		renderView(<AgentSettingsAPIKeysPageView {...defaultProps} />);

		await user.type(screen.getByLabelText("API Key"), "  sk-test-key  ");
		await user.click(screen.getByRole("button", { name: "Save" }));

		await waitFor(() => {
			expect(API.experimental.upsertUserAIProviderKey).toHaveBeenCalledWith(
				"prov-1",
				{ api_key: "sk-test-key" },
			);
		});
	});

	it("keeps the draft after a failed save so the user can retry", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "upsertUserAIProviderKey")
			.mockRejectedValueOnce(new Error("failed to save"))
			.mockResolvedValueOnce(savedKeyConfig);

		renderView(<AgentSettingsAPIKeysPageView {...defaultProps} />);

		await user.type(screen.getByLabelText("API Key"), "sk-test-key");
		await user.click(screen.getByRole("button", { name: "Save" }));

		await waitFor(() => {
			expect(API.experimental.upsertUserAIProviderKey).toHaveBeenCalledTimes(1);
		});

		await user.click(screen.getByRole("button", { name: "Save" }));

		await waitFor(() => {
			expect(API.experimental.upsertUserAIProviderKey).toHaveBeenCalledTimes(2);
		});
		expect(API.experimental.upsertUserAIProviderKey).toHaveBeenLastCalledWith(
			"prov-1",
			{ api_key: "sk-test-key" },
		);
	});

	it("calls the remove API when the user confirms removal", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "deleteUserAIProviderKey").mockResolvedValue(
			undefined,
		);

		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[
					{
						...baseProvider,
						has_user_api_key: true,
					},
				]}
			/>,
		);

		await user.click(screen.getByRole("button", { name: "Remove" }));
		const dialog = await screen.findByRole("dialog");
		await user.click(within(dialog).getByRole("button", { name: "Remove" }));

		await waitFor(() => {
			expect(API.experimental.deleteUserAIProviderKey).toHaveBeenCalledWith(
				"prov-1",
			);
		});
	});

	it("keeps the remove dialog open after a failed remove so the user can retry", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "deleteUserAIProviderKey")
			.mockRejectedValueOnce(new Error("failed to remove"))
			.mockResolvedValueOnce(undefined);

		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[
					{
						...baseProvider,
						has_user_api_key: true,
					},
				]}
			/>,
		);

		await user.click(screen.getByRole("button", { name: "Remove" }));
		const dialog = await screen.findByRole("dialog");
		const confirmButton = within(dialog).getByRole("button", {
			name: "Remove",
		});
		await user.click(confirmButton);

		await waitFor(() => {
			expect(API.experimental.deleteUserAIProviderKey).toHaveBeenCalledTimes(1);
		});

		await user.click(confirmButton);

		await waitFor(() => {
			expect(API.experimental.deleteUserAIProviderKey).toHaveBeenCalledTimes(2);
		});
	});
});

// Throwaway fixtures only: no test below uses a real credential.
const deviceGrant: AIDeviceGrantInitiateResponse = {
	grant_id: "grant-1",
	provider_id: "prov-chatgpt",
	user_code: "ABCD-1234",
	verification_uri: "https://auth.example.com/device",
	verification_uri_complete: "https://auth.example.com/device/ABCD-1234",
	expires_in: 900,
	poll_interval: 5,
	stores_access_token_only: false,
	refresh_supported: true,
	reauth_message: "Test re-auth message.",
};

const devicePoll = (
	overrides: Partial<AIDeviceGrantPollResponse> &
		Pick<AIDeviceGrantPollResponse, "status">,
): AIDeviceGrantPollResponse => ({
	grant_id: "grant-1",
	provider_id: "prov-chatgpt",
	user_code: "ABCD-1234",
	verification_uri: "https://auth.example.com/device",
	verification_uri_complete: "https://auth.example.com/device/ABCD-1234",
	expires_in: 900,
	poll_interval: 5,
	stores_access_token_only: false,
	refresh_supported: true,
	reauth_message: "Test re-auth message.",
	...overrides,
});

const chatgptProvider = createProvider({
	provider_id: "prov-chatgpt",
	provider: "openai",
	display_name: "ChatGPT",
	device_flow_supported: true,
});

describe("AgentSettingsAPIKeysPageView device-code sign-in", () => {
	it("offers sign-in only for device-flow providers", () => {
		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[
					chatgptProvider,
					createProvider({
						provider_id: "prov-plain",
						provider: "openai",
						display_name: "Plain keys",
					}),
				]}
			/>,
		);

		expect(
			screen.getByRole("button", { name: "Sign in with ChatGPT" }),
		).toBeInTheDocument();
		expect(
			screen.queryByRole("button", { name: "Sign in with Plain keys" }),
		).not.toBeInTheDocument();
	});

	it("starts a grant and shows the user code plus verification link", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "initiateUserAIDeviceGrant").mockResolvedValue(
			deviceGrant,
		);
		vi.spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue(
			devicePoll({ status: "pending" }),
		);

		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[chatgptProvider]}
			/>,
		);

		await user.click(
			screen.getByRole("button", { name: "Sign in with ChatGPT" }),
		);

		await waitFor(() => {
			expect(API.experimental.initiateUserAIDeviceGrant).toHaveBeenCalledWith(
				"prov-chatgpt",
			);
		});
		expect(screen.getByText("ABCD-1234")).toBeInTheDocument();
		expect(screen.getByRole("link")).toHaveAttribute(
			"href",
			"https://auth.example.com/device/ABCD-1234",
		);
		expect(screen.getByText(/Waiting for approval/)).toBeInTheDocument();
	});

	it("saves the approved token through the existing user-keys endpoint", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "initiateUserAIDeviceGrant").mockResolvedValue(
			deviceGrant,
		);
		vi.spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue(
			devicePoll({
				status: "authorized",
				api_key: "test-device-access-token",
			}),
		);
		vi.spyOn(API.experimental, "upsertUserAIProviderKey").mockResolvedValue(
			savedKeyConfig,
		);

		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[chatgptProvider]}
			/>,
		);

		await user.click(
			screen.getByRole("button", { name: "Sign in with ChatGPT" }),
		);

		await waitFor(() => {
			expect(API.experimental.upsertUserAIProviderKey).toHaveBeenCalledWith(
				"prov-chatgpt",
				{ api_key: "test-device-access-token" },
			);
		});
		// The token itself is never rendered.
		expect(
			screen.queryByText("test-device-access-token"),
		).not.toBeInTheDocument();
	});

	it("cancels the grant and resets the panel", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "initiateUserAIDeviceGrant").mockResolvedValue(
			deviceGrant,
		);
		vi.spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue(
			devicePoll({ status: "pending" }),
		);
		vi.spyOn(API.experimental, "cancelUserAIDeviceGrant").mockResolvedValue(
			undefined,
		);

		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[chatgptProvider]}
			/>,
		);

		await user.click(
			screen.getByRole("button", { name: "Sign in with ChatGPT" }),
		);
		await screen.findByText("ABCD-1234");
		await user.click(screen.getByRole("button", { name: "Cancel sign-in" }));

		await waitFor(() => {
			expect(API.experimental.cancelUserAIDeviceGrant).toHaveBeenCalledWith(
				"prov-chatgpt",
				"grant-1",
			);
		});
		await waitFor(() => {
			expect(screen.queryByText("ABCD-1234")).not.toBeInTheDocument();
		});
		expect(
			screen.getByRole("button", { name: "Sign in with ChatGPT" }),
		).toBeInTheDocument();
	});

	it("completes a server-persisted grant with no dashboard PUT", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "initiateUserAIDeviceGrant").mockResolvedValue(
			deviceGrant,
		);
		// Authorized with no api_key: the credential already reached the
		// key row server-side, so the dashboard must not PUT after it.
		vi.spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue(
			devicePoll({ status: "authorized" }),
		);
		const upsertSpy = vi
			.spyOn(API.experimental, "upsertUserAIProviderKey")
			.mockResolvedValue(savedKeyConfig);

		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[chatgptProvider]}
			/>,
		);

		await user.click(
			screen.getByRole("button", { name: "Sign in with ChatGPT" }),
		);

		await waitFor(() => {
			expect(
				screen.getByRole("button", { name: "Sign in with ChatGPT" }),
			).toBeInTheDocument();
		});
		expect(upsertSpy).not.toHaveBeenCalled();
		expect(screen.queryByText("ABCD-1234")).not.toBeInTheDocument();
	});

	it("surfaces expiry with the re-auth path", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "initiateUserAIDeviceGrant").mockResolvedValue(
			deviceGrant,
		);
		vi.spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue(
			devicePoll({ status: "expired" }),
		);

		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[chatgptProvider]}
			/>,
		);

		await user.click(
			screen.getByRole("button", { name: "Sign in with ChatGPT" }),
		);

		await screen.findByText("Test re-auth message.");
		expect(
			screen.getByRole("button", { name: "Start over" }),
		).toBeInTheDocument();
	});
});

describe("AgentSettingsAPIKeysPageView OAuth refresh state", () => {
	it("shows expiry and auto-refresh for an OAuth-derived key", () => {
		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[
					{
						...chatgptProvider,
						has_user_api_key: true,
						oauth_expiry: "2030-05-01T12:00:00Z",
						refresh_supported: true,
					},
				]}
			/>,
		);

		expect(screen.getByText("Key saved")).toBeInTheDocument();
		// The expiry note is the only copy naming the access token: the
		// sign-in panel below also mentions automatic refresh.
		expect(
			screen.getByText(/Access token expires.*refreshes it automatically/),
		).toBeInTheDocument();
	});

	it("shows no expiry or refresh claims for a static key", () => {
		renderView(
			<AgentSettingsAPIKeysPageView
				{...defaultProps}
				providers={[{ ...baseProvider, has_user_api_key: true }]}
			/>,
		);

		expect(screen.getByText("Key saved")).toBeInTheDocument();
		expect(screen.queryByText(/Access token expires/)).not.toBeInTheDocument();
		expect(
			screen.queryByText(/refreshes it automatically/),
		).not.toBeInTheDocument();
	});

	it("renders exactly one re-auth prompt and never a saved-key badge", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "initiateUserAIDeviceGrant").mockResolvedValue(
			deviceGrant,
		);
		vi.spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue(
			devicePoll({ status: "pending" }),
		);

		const queryClient = createTestQueryClient();
		const view = () => (
			<QueryClientProvider client={queryClient}>
				<AgentSettingsAPIKeysPageView
					{...defaultProps}
					providers={[
						{
							...chatgptProvider,
							has_user_api_key: true,
							reauth_required: true,
						},
					]}
				/>
			</QueryClientProvider>
		);
		const { rerender } = render(view());

		expect(screen.getByText("Sign-in expired")).toBeInTheDocument();
		expect(screen.queryByText("Key saved")).not.toBeInTheDocument();
		expect(
			screen.queryByText(/The shared deployment key is being used/),
		).not.toBeInTheDocument();

		// Exactly one prompt, reusing the device-grant initiate path.
		const prompts = screen.getAllByRole("button", {
			name: "Sign in again with ChatGPT",
		});
		expect(prompts).toHaveLength(1);

		// A second identical failure re-renders the same banner instead of
		// stacking another prompt.
		rerender(view());
		expect(
			screen.getAllByRole("button", { name: "Sign in again with ChatGPT" }),
		).toHaveLength(1);

		await user.click(
			screen.getByRole("button", { name: "Sign in again with ChatGPT" }),
		);
		await waitFor(() => {
			expect(API.experimental.initiateUserAIDeviceGrant).toHaveBeenCalledWith(
				"prov-chatgpt",
			);
		});
		await screen.findByText("ABCD-1234");
	});
});
