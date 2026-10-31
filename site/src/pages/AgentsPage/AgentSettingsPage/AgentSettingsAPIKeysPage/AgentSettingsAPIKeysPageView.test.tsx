import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import type {
	AIBrowserGrantExchangeResponse,
	AIBrowserGrantInitiateResponse,
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
import {
	BROWSER_CALLBACK_EMPTY_ERROR,
	BROWSER_CALLBACK_MALFORMED_ERROR,
	parseBrowserCallbackInput,
} from "./parseBrowserCallbackInput";

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
	browser_flow_supported: overrides.browser_flow_supported ?? false,
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
	browser_flow_supported: false,
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

const renderView = (ui: React.ReactElement) => {
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
const mockDeviceGrant: AIDeviceGrantInitiateResponse = {
	grant_id: "grant-1",
	provider_id: "prov-chatgpt",
	user_code: "ABCD-1234",
	verification_uri: "https://auth.example.com/device",
	verification_uri_complete: "https://auth.example.com/device/ABCD-1234",
	expires_in: 900,
	poll_interval: 1,
	stores_access_token_only: false,
	refresh_supported: true,
	reauth_message: "Test re-auth message.",
};

const mockDevicePoll: AIDeviceGrantPollResponse = {
	...mockDeviceGrant,
	status: "pending",
};

const mockChatGPTProvider = createProvider({
	provider_id: "prov-chatgpt",
	provider: "openai",
	display_name: "ChatGPT",
	device_flow_supported: true,
	browser_flow_supported: true,
});

const mockBrowserGrant: AIBrowserGrantInitiateResponse = {
	grant_id: "bgrant-1",
	provider_id: "prov-chatgpt",
	authorize_url:
		"https://auth.example.com/oauth/authorize?code_challenge=test&state=test-state",
	expires_in: 900,
	stores_access_token_only: false,
	refresh_supported: true,
	reauth_message: "Test browser re-auth message.",
};

const mockBrowserExchange: AIBrowserGrantExchangeResponse = {
	grant_id: "bgrant-1",
	provider_id: "prov-chatgpt",
	status: "authorized",
	expires_in: 900,
	stores_access_token_only: false,
	refresh_supported: true,
	reauth_message: "Test browser re-auth message.",
};

const FULL_CALLBACK =
	"http://localhost:1455/auth/callback?code=test-code&state=test-state";

const renderChatGPT = (
	provider: UserChatProviderConfig = mockChatGPTProvider,
) =>
	renderView(
		<AgentSettingsAPIKeysPageView {...defaultProps} providers={[provider]} />,
	);

const startDeviceGrant = async (
	user: ReturnType<typeof userEvent.setup>,
	poll: AIDeviceGrantPollResponse,
) => {
	vi.spyOn(API.experimental, "initiateUserAIDeviceGrant").mockResolvedValue(
		mockDeviceGrant,
	);
	vi.spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue(poll);
	renderChatGPT();
	await user.click(
		screen.getByRole("button", { name: "Sign in with ChatGPT" }),
	);
};

const startBrowserGrant = async (user: ReturnType<typeof userEvent.setup>) => {
	vi.spyOn(API.experimental, "initiateUserAIBrowserGrant").mockResolvedValue(
		mockBrowserGrant,
	);
	vi.spyOn(window, "open").mockReturnValue(null);
	renderChatGPT();
	await user.click(
		screen.getByRole("button", {
			name: "Sign in with ChatGPT in your browser",
		}),
	);
	await screen.findByLabelText("Authorization callback");
};

describe("AgentSettingsAPIKeysPageView device-code sign-in", () => {
	it("starts a grant and links the bare verification URI", async () => {
		const user = userEvent.setup();
		await startDeviceGrant(user, mockDevicePoll);

		await waitFor(() => {
			expect(API.experimental.initiateUserAIDeviceGrant).toHaveBeenCalledWith(
				"prov-chatgpt",
			);
		});
		expect(
			await screen.findByRole("link", { name: /auth\.example\.com\/device/ }),
		).toHaveAttribute("href", mockDeviceGrant.verification_uri);
	});

	it("saves the approved token through the user-keys endpoint without rendering it", async () => {
		const user = userEvent.setup();
		const upsertSpy = vi
			.spyOn(API.experimental, "upsertUserAIProviderKey")
			.mockResolvedValue(savedKeyConfig);
		await startDeviceGrant(user, {
			...mockDevicePoll,
			status: "authorized",
			api_key: "test-device-access-token",
		});

		await waitFor(() => {
			expect(upsertSpy).toHaveBeenCalledWith("prov-chatgpt", {
				api_key: "test-device-access-token",
			});
		});
		expect(document.body).not.toHaveTextContent("test-device-access-token");
	});

	it("saves a failed sign-in key once and waits for Start over", async () => {
		const user = userEvent.setup();
		const upsertSpy = vi
			.spyOn(API.experimental, "upsertUserAIProviderKey")
			.mockRejectedValue(new Error("save failed"));
		const cancelSpy = vi.spyOn(API.experimental, "cancelUserAIDeviceGrant");
		await startDeviceGrant(user, {
			...mockDevicePoll,
			status: "authorized",
			api_key: "test-device-access-token",
		});

		await user.click(await screen.findByRole("button", { name: "Start over" }));

		await screen.findByRole("button", { name: "Sign in with ChatGPT" });
		expect(upsertSpy).toHaveBeenCalledTimes(1);
		expect(cancelSpy).not.toHaveBeenCalled();
	});

	it("completes a server-persisted grant with no dashboard PUT", async () => {
		const user = userEvent.setup();
		const upsertSpy = vi.spyOn(API.experimental, "upsertUserAIProviderKey");
		await startDeviceGrant(user, { ...mockDevicePoll, status: "authorized" });

		await waitFor(() => {
			expect(API.experimental.getUserAIDeviceGrant).toHaveBeenCalled();
		});
		await screen.findByRole("button", { name: "Sign in with ChatGPT" });
		expect(upsertSpy).not.toHaveBeenCalled();
	});

	it("keeps polling when the first poll fails", async () => {
		const user = userEvent.setup();
		const upsertSpy = vi
			.spyOn(API.experimental, "upsertUserAIProviderKey")
			.mockResolvedValue(savedKeyConfig);
		vi.spyOn(API.experimental, "initiateUserAIDeviceGrant").mockResolvedValue(
			mockDeviceGrant,
		);
		const pollSpy = vi
			.spyOn(API.experimental, "getUserAIDeviceGrant")
			.mockRejectedValueOnce(new Error("network"))
			.mockResolvedValue({
				...mockDevicePoll,
				status: "authorized",
				api_key: "test-device-access-token",
			});
		renderChatGPT();
		await user.click(
			screen.getByRole("button", { name: "Sign in with ChatGPT" }),
		);

		await waitFor(
			() => {
				expect(upsertSpy).toHaveBeenCalledTimes(1);
			},
			{ timeout: 3000 },
		);
		expect(pollSpy).toHaveBeenCalledTimes(2);
	});

	it("cancels a pending grant and resets the panel", async () => {
		const user = userEvent.setup();
		const cancelSpy = vi
			.spyOn(API.experimental, "cancelUserAIDeviceGrant")
			.mockResolvedValue(undefined);
		await startDeviceGrant(user, mockDevicePoll);

		await user.click(
			await screen.findByRole("button", { name: "Cancel sign-in" }),
		);

		await waitFor(() => {
			expect(cancelSpy).toHaveBeenCalledWith("prov-chatgpt", "grant-1");
		});
		await screen.findByRole("button", { name: "Sign in with ChatGPT" });
	});

	it("resets the panel when the cancel request fails", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "cancelUserAIDeviceGrant").mockRejectedValue(
			new Error("already terminal"),
		);
		await startDeviceGrant(user, mockDevicePoll);

		await user.click(
			await screen.findByRole("button", { name: "Cancel sign-in" }),
		);

		await screen.findByRole("button", { name: "Sign in with ChatGPT" });
	});

	it("starts over from an expired grant without a cancel request", async () => {
		const user = userEvent.setup();
		const cancelSpy = vi.spyOn(API.experimental, "cancelUserAIDeviceGrant");
		await startDeviceGrant(user, { ...mockDevicePoll, status: "expired" });

		await user.click(await screen.findByRole("button", { name: "Start over" }));

		await screen.findByRole("button", { name: "Sign in with ChatGPT" });
		expect(cancelSpy).not.toHaveBeenCalled();
	});

	it("re-authenticates through the device-grant initiate path", async () => {
		const user = userEvent.setup();
		const initiateSpy = vi
			.spyOn(API.experimental, "initiateUserAIDeviceGrant")
			.mockResolvedValue(mockDeviceGrant);
		vi.spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue(
			mockDevicePoll,
		);
		renderChatGPT({
			...mockChatGPTProvider,
			has_user_api_key: true,
			reauth_required: true,
		});

		await user.click(
			screen.getByRole("button", { name: "Sign in again with ChatGPT" }),
		);

		await waitFor(() => {
			expect(initiateSpy).toHaveBeenCalledWith("prov-chatgpt");
		});
	});
});

describe("AgentSettingsAPIKeysPageView browser sign-in", () => {
	it("opens the provider authorize URL directly", async () => {
		const user = userEvent.setup();
		await startBrowserGrant(user);

		expect(API.experimental.initiateUserAIBrowserGrant).toHaveBeenCalledWith(
			"prov-chatgpt",
		);
		expect(window.open).toHaveBeenCalledTimes(1);
		expect(window.open).toHaveBeenCalledWith(
			mockBrowserGrant.authorize_url,
			"_blank",
			"noopener,noreferrer",
		);
		expect(
			screen.getByRole("link", { name: /ChatGPT sign-in/ }),
		).toHaveAttribute("href", mockBrowserGrant.authorize_url);
	});

	it("does not open a popup when authorize_url is empty", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "initiateUserAIBrowserGrant").mockResolvedValue({
			...mockBrowserGrant,
			authorize_url: "",
		});
		const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
		renderChatGPT();

		await user.click(
			screen.getByRole("button", {
				name: "Sign in with ChatGPT in your browser",
			}),
		);

		await waitFor(() => {
			expect(API.experimental.initiateUserAIBrowserGrant).toHaveBeenCalled();
		});
		expect(openSpy).not.toHaveBeenCalled();
	});

	it("exchanges the pasted callback with no dashboard PUT", async () => {
		const user = userEvent.setup();
		const exchangeSpy = vi
			.spyOn(API.experimental, "exchangeUserAIBrowserGrant")
			.mockResolvedValue(mockBrowserExchange);
		const upsertSpy = vi.spyOn(API.experimental, "upsertUserAIProviderKey");
		await startBrowserGrant(user);

		await user.type(
			screen.getByLabelText("Authorization callback"),
			FULL_CALLBACK,
		);
		await user.click(screen.getByRole("button", { name: "Complete sign-in" }));

		await waitFor(() => {
			expect(exchangeSpy).toHaveBeenCalledWith("prov-chatgpt", "bgrant-1", {
				input: FULL_CALLBACK,
			});
		});
		await screen.findByRole("button", {
			name: "Sign in with ChatGPT in your browser",
		});
		expect(upsertSpy).not.toHaveBeenCalled();
	});

	it("keeps the paste after a failed exchange so the user can retry", async () => {
		const user = userEvent.setup();
		const exchangeSpy = vi
			.spyOn(API.experimental, "exchangeUserAIBrowserGrant")
			.mockRejectedValueOnce(new Error("bad code"))
			.mockResolvedValueOnce(mockBrowserExchange);
		await startBrowserGrant(user);
		const field = screen.getByLabelText("Authorization callback");

		await user.type(field, "bad-code");
		await user.click(screen.getByRole("button", { name: "Complete sign-in" }));
		await waitFor(() => {
			expect(exchangeSpy).toHaveBeenCalledTimes(1);
		});
		expect(field).toHaveValue("bad-code");

		await user.clear(field);
		await user.type(field, "good-code");
		await user.click(screen.getByRole("button", { name: "Complete sign-in" }));

		await waitFor(() => {
			expect(exchangeSpy).toHaveBeenLastCalledWith("prov-chatgpt", "bgrant-1", {
				input: "good-code",
			});
		});
	});

	it("cancels the grant and resets the panel", async () => {
		const user = userEvent.setup();
		const cancelSpy = vi
			.spyOn(API.experimental, "cancelUserAIBrowserGrant")
			.mockResolvedValue(undefined);
		await startBrowserGrant(user);

		await user.click(screen.getByRole("button", { name: "Cancel sign-in" }));

		await waitFor(() => {
			expect(cancelSpy).toHaveBeenCalledWith("prov-chatgpt", "bgrant-1");
		});
		await screen.findByRole("button", {
			name: "Sign in with ChatGPT in your browser",
		});
	});

	it("disables Cancel sign-in while the exchange is in flight", async () => {
		const user = userEvent.setup();
		const exchange = Promise.withResolvers<AIBrowserGrantExchangeResponse>();
		vi.spyOn(API.experimental, "exchangeUserAIBrowserGrant").mockReturnValue(
			exchange.promise,
		);
		const cancelSpy = vi.spyOn(API.experimental, "cancelUserAIBrowserGrant");
		await startBrowserGrant(user);

		await user.type(
			screen.getByLabelText("Authorization callback"),
			FULL_CALLBACK,
		);
		await user.click(screen.getByRole("button", { name: "Complete sign-in" }));

		await waitFor(() => {
			expect(
				screen.getByRole("button", { name: "Cancel sign-in" }),
			).toBeDisabled();
		});
		exchange.resolve(mockBrowserExchange);
		await screen.findByRole("button", {
			name: "Sign in with ChatGPT in your browser",
		});
		expect(cancelSpy).not.toHaveBeenCalled();
	});

	it("starts over from an expired exchange without a cancel request", async () => {
		const user = userEvent.setup();
		vi.spyOn(API.experimental, "exchangeUserAIBrowserGrant").mockResolvedValue({
			...mockBrowserExchange,
			status: "expired",
		});
		const cancelSpy = vi.spyOn(API.experimental, "cancelUserAIBrowserGrant");
		await startBrowserGrant(user);

		await user.type(
			screen.getByLabelText("Authorization callback"),
			"stale-code",
		);
		await user.click(screen.getByRole("button", { name: "Complete sign-in" }));
		await user.click(await screen.findByRole("button", { name: "Start over" }));

		await screen.findByRole("button", {
			name: "Sign in with ChatGPT in your browser",
		});
		expect(cancelSpy).not.toHaveBeenCalled();
	});

	it("sends a trimmed full URL when the paste is whitespace-padded", async () => {
		const user = userEvent.setup();
		const exchangeSpy = vi
			.spyOn(API.experimental, "exchangeUserAIBrowserGrant")
			.mockResolvedValue(mockBrowserExchange);
		await startBrowserGrant(user);

		await user.type(
			screen.getByLabelText("Authorization callback"),
			`  ${FULL_CALLBACK}\n`,
		);
		await user.click(screen.getByRole("button", { name: "Complete sign-in" }));

		await waitFor(() => {
			expect(exchangeSpy).toHaveBeenCalledWith("prov-chatgpt", "bgrant-1", {
				input: FULL_CALLBACK,
			});
		});
	});

	it("sends a bare code without requiring the full URL", async () => {
		const user = userEvent.setup();
		const exchangeSpy = vi
			.spyOn(API.experimental, "exchangeUserAIBrowserGrant")
			.mockResolvedValue(mockBrowserExchange);
		await startBrowserGrant(user);

		await user.type(
			screen.getByLabelText("Authorization callback"),
			"test-code-123",
		);
		await user.click(screen.getByRole("button", { name: "Complete sign-in" }));

		await waitFor(() => {
			expect(exchangeSpy).toHaveBeenCalledWith("prov-chatgpt", "bgrant-1", {
				input: "test-code-123",
			});
		});
	});

	it("reports malformed input and never calls exchange", async () => {
		const user = userEvent.setup();
		const exchangeSpy = vi.spyOn(
			API.experimental,
			"exchangeUserAIBrowserGrant",
		);
		await startBrowserGrant(user);

		await user.type(
			screen.getByLabelText("Authorization callback"),
			"not a callback!!!",
		);
		await user.click(screen.getByRole("button", { name: "Complete sign-in" }));

		expect(await screen.findByRole("alert")).toHaveTextContent(
			BROWSER_CALLBACK_MALFORMED_ERROR,
		);
		expect(exchangeSpy).not.toHaveBeenCalled();
	});

	it("reports an empty submit and never calls exchange", async () => {
		const user = userEvent.setup();
		const exchangeSpy = vi.spyOn(
			API.experimental,
			"exchangeUserAIBrowserGrant",
		);
		await startBrowserGrant(user);

		await user.click(screen.getByRole("button", { name: "Complete sign-in" }));

		expect(await screen.findByRole("alert")).toHaveTextContent(
			BROWSER_CALLBACK_EMPTY_ERROR,
		);
		expect(exchangeSpy).not.toHaveBeenCalled();
	});

	it("auto-focuses the callback field once a grant exists", async () => {
		const user = userEvent.setup();
		await startBrowserGrant(user);

		expect(screen.getByLabelText("Authorization callback")).toHaveFocus();
	});

	it("fills the field from the clipboard", async () => {
		const user = userEvent.setup();
		vi.spyOn(navigator.clipboard, "readText").mockResolvedValue(FULL_CALLBACK);
		await startBrowserGrant(user);

		await user.click(
			screen.getByRole("button", { name: "Paste from clipboard" }),
		);

		await waitFor(() => {
			expect(screen.getByLabelText("Authorization callback")).toHaveValue(
				FULL_CALLBACK,
			);
		});
	});

	it("keeps manual entry working when the clipboard read fails", async () => {
		const user = userEvent.setup();
		vi.spyOn(navigator.clipboard, "readText").mockRejectedValue(
			new Error("denied"),
		);
		await startBrowserGrant(user);

		await user.click(
			screen.getByRole("button", { name: "Paste from clipboard" }),
		);
		await user.type(
			screen.getByLabelText("Authorization callback"),
			"test-code-123",
		);

		expect(screen.getByLabelText("Authorization callback")).toHaveValue(
			"test-code-123",
		);
	});
});

describe("parseBrowserCallbackInput", () => {
	it("accepts the full callback URL", () => {
		expect(parseBrowserCallbackInput(FULL_CALLBACK)).toEqual({
			ok: true,
			input: FULL_CALLBACK,
		});
	});

	it("accepts a bare code", () => {
		expect(parseBrowserCallbackInput("test-code-123")).toEqual({
			ok: true,
			input: "test-code-123",
		});
	});

	it("accepts the code#state shorthand", () => {
		expect(parseBrowserCallbackInput("test-code#test-state")).toEqual({
			ok: true,
			input: "test-code#test-state",
		});
	});

	it("trims whitespace and newlines around a full URL", () => {
		expect(parseBrowserCallbackInput(`  ${FULL_CALLBACK}\n`)).toEqual({
			ok: true,
			input: FULL_CALLBACK,
		});
	});

	it("rejects an empty paste with a specific error", () => {
		expect(parseBrowserCallbackInput("   \n ")).toEqual({
			ok: false,
			error: BROWSER_CALLBACK_EMPTY_ERROR,
		});
	});

	it.each([
		"not a callback!!!",
		"http://localhost:1455/auth/callback?state=test-state",
		"localhost",
		"state=test-state",
	])("rejects %s with a specific error", (input) => {
		expect(parseBrowserCallbackInput(input)).toEqual({
			ok: false,
			error: BROWSER_CALLBACK_MALFORMED_ERROR,
		});
	});
});
