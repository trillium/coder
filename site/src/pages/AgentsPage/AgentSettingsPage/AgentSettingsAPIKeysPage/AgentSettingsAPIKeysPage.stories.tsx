import type { Meta, StoryObj } from "@storybook/react-vite";
import { spyOn, userEvent, within } from "storybook/test";
import { API } from "#/api/api";
import type {
	AIBrowserGrantInitiateResponse,
	AIDeviceGrantInitiateResponse,
	AIDeviceGrantPollResponse,
	ChatModel,
	UserChatProviderConfig,
} from "#/api/typesGenerated";
import { MockChatModel } from "#/testHelpers/chatModels";
import { AgentSettingsAPIKeysPageView } from "./AgentSettingsAPIKeysPageView";

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

const createModel = (
	overrides: Partial<ChatModel> &
		Pick<ChatModel, "id" | "ai_provider_id" | "model">,
): ChatModel => ({
	...MockChatModel,
	created_at: "2026-03-01T00:00:00.000Z",
	updated_at: "2026-03-01T00:00:00.000Z",
	...overrides,
});

const baseProvider = createProvider({
	provider_id: "prov-1",
	provider: "openai",
	display_name: "OpenAI",
});

const baseModel = createModel({
	id: "model-1",
	ai_provider_id: "prov-1",
	display_name: "GPT-4o",
	model: "gpt-4o",
});

const baseModels = [baseModel];

const meta = {
	title: "pages/AgentsPage/AgentSettingsAPIKeysPageView",
	component: AgentSettingsAPIKeysPageView,
	args: {
		error: undefined,
		isLoading: false,
		providers: [baseProvider],
		models: baseModels,
		isModelsLoading: false,
		areModelsUnavailable: false,
	},
} satisfies Meta<typeof AgentSettingsAPIKeysPageView>;

export default meta;
type Story = StoryObj<typeof AgentSettingsAPIKeysPageView>;

export const Default: Story = {};

export const WithSavedKey: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-1",
				provider: "openai",
				display_name: "OpenAI",
				has_user_api_key: true,
				has_central_api_key_fallback: true,
			}),
		],
		models: baseModels,
	},
};

export const WithOAuthExpiry: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-1",
				provider: "openai",
				display_name: "ChatGPT",
				has_user_api_key: true,
				device_flow_supported: true,
				oauth_expiry: "2030-05-01T12:00:00.000Z",
				refresh_supported: true,
			}),
		],
		models: baseModels,
	},
};

export const WithReauthRequired: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-1",
				provider: "openai",
				display_name: "ChatGPT",
				has_user_api_key: true,
				device_flow_supported: true,
				reauth_required: true,
			}),
		],
		models: baseModels,
	},
};

export const WithBrowserSignIn: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-1",
				provider: "openai",
				display_name: "ChatGPT",
				device_flow_supported: true,
				browser_flow_supported: true,
			}),
		],
		models: baseModels,
	},
};

const chatGPTProvider = createProvider({
	provider_id: "prov-1",
	provider: "openai",
	display_name: "ChatGPT",
	device_flow_supported: true,
	browser_flow_supported: true,
	refresh_supported: true,
});

const deviceGrant: AIDeviceGrantInitiateResponse = {
	grant_id: "grant-1",
	provider_id: "prov-1",
	user_code: "ABCD-1234",
	verification_uri: "https://auth.openai.com/codex/device",
	verification_uri_complete: "https://auth.openai.com/codex/device",
	expires_in: 900,
	poll_interval: 5,
	stores_access_token_only: false,
	refresh_supported: true,
	reauth_message: "",
};

const pendingDevicePoll: AIDeviceGrantPollResponse = {
	...deviceGrant,
	status: "pending",
};

const browserGrant: AIBrowserGrantInitiateResponse = {
	grant_id: "bgrant-1",
	provider_id: "prov-1",
	authorize_url: "https://auth.openai.com/oauth/authorize?state=story",
	expires_in: 900,
	stores_access_token_only: false,
	refresh_supported: true,
	reauth_message: "",
};

const clickDeviceSignIn = async (canvasElement: HTMLElement) => {
	spyOn(API.experimental, "initiateUserAIDeviceGrant").mockResolvedValue(
		deviceGrant,
	);
	await userEvent.click(
		await within(canvasElement).findByRole("button", {
			name: "Sign in with ChatGPT",
		}),
	);
};

const clickBrowserSignIn = async (canvasElement: HTMLElement) => {
	spyOn(API.experimental, "initiateUserAIBrowserGrant").mockResolvedValue(
		browserGrant,
	);
	spyOn(window, "open").mockReturnValue(null);
	await userEvent.click(
		await within(canvasElement).findByRole("button", {
			name: "Sign in with ChatGPT in your browser",
		}),
	);
};

const submitCallback = async (canvasElement: HTMLElement, input: string) => {
	const canvas = within(canvasElement);
	const field = await canvas.findByLabelText("Authorization callback");
	if (input) {
		await userEvent.type(field, input);
	}
	await userEvent.click(
		canvas.getByRole("button", { name: "Complete sign-in" }),
	);
};

export const DeviceCodeSignInPending: Story = {
	args: { providers: [chatGPTProvider] },
	play: async ({ canvasElement }) => {
		spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue(
			pendingDevicePoll,
		);
		await clickDeviceSignIn(canvasElement);
	},
};

export const DeviceCodeSignInPollError: Story = {
	args: { providers: [chatGPTProvider] },
	play: async ({ canvasElement }) => {
		spyOn(API.experimental, "getUserAIDeviceGrant").mockRejectedValue(
			new Error("Network Error"),
		);
		await clickDeviceSignIn(canvasElement);
	},
};

export const DeviceCodeSignInExpired: Story = {
	args: { providers: [chatGPTProvider] },
	play: async ({ canvasElement }) => {
		spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue({
			...pendingDevicePoll,
			status: "expired",
		});
		await clickDeviceSignIn(canvasElement);
	},
};

export const DeviceCodeSignInSaveFailed: Story = {
	args: { providers: [chatGPTProvider] },
	play: async ({ canvasElement }) => {
		spyOn(API.experimental, "getUserAIDeviceGrant").mockResolvedValue({
			...pendingDevicePoll,
			status: "authorized",
			api_key: "story-access-token",
		});
		spyOn(API.experimental, "upsertUserAIProviderKey").mockRejectedValue(
			new Error("save failed"),
		);
		await clickDeviceSignIn(canvasElement);
	},
};

export const BrowserSignInAwaitingCallback: Story = {
	args: { providers: [chatGPTProvider] },
	play: async ({ canvasElement }) => {
		await clickBrowserSignIn(canvasElement);
	},
};

export const BrowserSignInInvalidCallback: Story = {
	args: { providers: [chatGPTProvider] },
	play: async ({ canvasElement }) => {
		await clickBrowserSignIn(canvasElement);
		await submitCallback(canvasElement, "not a callback!!!");
	},
};

export const BrowserSignInExchanging: Story = {
	args: { providers: [chatGPTProvider] },
	play: async ({ canvasElement }) => {
		spyOn(API.experimental, "exchangeUserAIBrowserGrant").mockReturnValue(
			new Promise(() => {}),
		);
		await clickBrowserSignIn(canvasElement);
		await submitCallback(canvasElement, "story-code");
	},
};

export const BrowserSignInExpired: Story = {
	args: { providers: [chatGPTProvider] },
	play: async ({ canvasElement }) => {
		spyOn(API.experimental, "exchangeUserAIBrowserGrant").mockResolvedValue({
			grant_id: "bgrant-1",
			provider_id: "prov-1",
			status: "expired",
			expires_in: 0,
			stores_access_token_only: false,
			refresh_supported: true,
			reauth_message: "",
		});
		await clickBrowserSignIn(canvasElement);
		await submitCallback(canvasElement, "story-code");
	},
};

export const WithRefetchError: Story = {
	args: {
		providers: [chatGPTProvider],
		error: new Error("Failed to refresh providers."),
	},
};

export const UserKeysDisabled: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-1",
				provider: "openai",
				display_name: "OpenAI",
				byok_enabled: false,
				device_flow_supported: false,
				browser_flow_supported: false,
				refresh_supported: false,
				reauth_required: false,
				has_central_api_key_fallback: true,
			}),
		],
	},
};

export const WithFallback: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-1",
				provider: "anthropic",
				display_name: "Anthropic",
				has_central_api_key_fallback: true,
			}),
		],
		models: [
			createModel({
				id: "model-1",
				ai_provider_id: "prov-1",
				display_name: "Claude Sonnet 4",
				model: "claude-sonnet-4-20250514",
			}),
		],
	},
};

export const MultipleProviders: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-openai",
				provider: "openai",
				display_name: "OpenAI",
				has_user_api_key: true,
				has_central_api_key_fallback: true,
			}),
			createProvider({
				provider_id: "prov-anthropic",
				provider: "anthropic",
				display_name: "Anthropic",
				has_central_api_key_fallback: true,
			}),
			createProvider({
				provider_id: "prov-google",
				provider: "google",
				display_name: "Google",
			}),
		],
		models: [
			createModel({
				id: "model-openai-1",
				ai_provider_id: "prov-openai",
				display_name: "GPT-4o",
				model: "gpt-4o",
			}),
			createModel({
				id: "model-anthropic-1",
				ai_provider_id: "prov-anthropic",
				display_name: "Claude Sonnet 4",
				model: "claude-sonnet-4-20250514",
			}),
			createModel({
				id: "model-anthropic-2",
				ai_provider_id: "prov-anthropic",
				display_name: "Claude Opus 4",
				model: "claude-opus-4-20250514",
			}),
		],
	},
};

export const Empty: Story = {
	args: {
		providers: [],
		models: [],
	},
};

export const Loading: Story = {
	args: {
		isLoading: true,
		providers: [],
		models: [],
	},
};

export const ModelsUnavailable: Story = {
	args: {
		areModelsUnavailable: true,
		models: [],
	},
};

export const ModelsLoading: Story = {
	args: {
		isModelsLoading: true,
		models: [],
	},
};

export const SomeModelsUnavailable: Story = {
	args: {
		areModelsUnavailable: true,
	},
};

export const SavingSingleProvider: Story = {
	args: {
		providers: [
			baseProvider,
			createProvider({
				provider_id: "prov-2",
				provider: "anthropic",
				display_name: "Anthropic",
			}),
		],
		models: [
			...baseModels,
			createModel({
				id: "model-2",
				ai_provider_id: "prov-2",
				display_name: "Claude Sonnet 4",
				model: "claude-sonnet-4-20250514",
			}),
		],
	},
	play: async ({ canvasElement }) => {
		spyOn(API.experimental, "upsertUserAIProviderKey").mockImplementation(
			() => new Promise(() => {}),
		);
		const canvas = within(canvasElement);
		const panel = within(
			await canvas.findByRole("article", { name: "OpenAI" }),
		);
		await userEvent.type(panel.getByLabelText("API Key"), "sk-test-key");
		await userEvent.click(panel.getByRole("button", { name: "Save" }));
	},
};

export const RemovesProviderKey: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-1",
				provider: "openai",
				display_name: "OpenAI",
				has_user_api_key: true,
			}),
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(
			await canvas.findByRole("button", { name: "Remove" }),
		);
	},
};

export const ClearsMaskedApiKeyOnFocus: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-1",
				provider: "openai",
				display_name: "OpenAI",
				has_user_api_key: true,
			}),
		],
	},
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(await canvas.findByLabelText("API Key"));
	},
};

export const ShowsProviderStatuses: Story = {
	args: {
		providers: [
			createProvider({
				provider_id: "prov-openai",
				provider: "openai",
				display_name: "OpenAI",
				has_user_api_key: true,
				has_central_api_key_fallback: false,
			}),
			createProvider({
				provider_id: "prov-anthropic",
				provider: "anthropic",
				display_name: "Anthropic",
				has_user_api_key: false,
				has_central_api_key_fallback: true,
			}),
			createProvider({
				provider_id: "prov-google",
				provider: "google",
				display_name: "Google",
				has_user_api_key: false,
				has_central_api_key_fallback: false,
			}),
		],
		models: [
			createModel({
				id: "model-openai-1",
				ai_provider_id: "prov-openai",
				display_name: "GPT-4o",
				model: "gpt-4o",
			}),
			createModel({
				id: "model-anthropic-1",
				ai_provider_id: "prov-anthropic",
				display_name: "Claude Sonnet 4",
				model: "claude-sonnet-4-20250514",
			}),
			createModel({
				id: "model-google-1",
				ai_provider_id: "prov-google",
				display_name: "Gemini 2.5 Pro",
				model: "gemini-2.5-pro",
			}),
		],
	},
};

export const WithError: Story = {
	args: {
		error: new Error("Failed to load provider configurations"),
	},
};
