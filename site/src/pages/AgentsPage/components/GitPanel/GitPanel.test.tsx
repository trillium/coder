import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { FC, PropsWithChildren } from "react";
import { QueryClientProvider } from "react-query";
import { describe, expect, it, vi } from "vitest";
import { API } from "#/api/api";
import type { ChatDiffContents } from "#/api/typesGenerated";
import { TooltipProvider } from "#/components/Tooltip/Tooltip";
import { ThemeOverride } from "#/contexts/ThemeProvider";
import { MockChatDiffStatus } from "#/testHelpers/chatEntities";
import { createTestQueryClient } from "#/testHelpers/renderHelpers";
import themes, { DEFAULT_THEME } from "#/theme";
import { GitPanel } from "./GitPanel";

const diffContents = (chatId: string): ChatDiffContents => ({
	chat_id: chatId,
});

const Wrapper: FC<PropsWithChildren> = ({ children }) => {
	const queryClient = createTestQueryClient();
	return (
		<QueryClientProvider client={queryClient}>
			<ThemeOverride theme={themes[DEFAULT_THEME]}>
				<TooltipProvider>{children}</TooltipProvider>
			</ThemeOverride>
		</QueryClientProvider>
	);
};

const renderPanel = (props: Partial<React.ComponentProps<typeof GitPanel>>) => {
	return render(
		<Wrapper>
			<GitPanel
				chatId="test-chat"
				onRefresh={() => true}
				onCommit={() => {}}
				repositories={new Map()}
				{...props}
			/>
		</Wrapper>,
	);
};

describe("GitPanel per-ref views", () => {
	it("fetches the selected ref's diff, not the primary's", async () => {
		const user = userEvent.setup();
		const getDiff = vi
			.spyOn(API.experimental, "getChatDiffContents")
			.mockResolvedValue(diffContents("test-chat"));

		renderPanel({
			remoteDiffStats: [
				{
					...MockChatDiffStatus,
					pull_request_title: "feat: first change",
					git_branch: "feat/first",
					pr_number: 23020,
					url: "https://github.com/coder/coder/pull/23020",
				},
				{
					...MockChatDiffStatus,
					pull_request_title: "fix: second change",
					git_branch: "fix/second",
					pr_number: 23021,
					url: "https://github.com/coder/coder/pull/23021",
					pull_request_state: "merged",
				},
			],
		});

		// The default view targets the primary ref.
		await waitFor(() =>
			expect(getDiff).toHaveBeenCalledWith(
				"test-chat",
				expect.objectContaining({
					remote_origin: "https://github.com/coder/coder",
					git_branch: "feat/first",
				}),
			),
		);

		await user.click(screen.getByRole("button", { name: "Switch git view" }));
		const menu = await screen.findByRole("menu");
		await user.click(within(menu).getByText("PR #23021"));

		// After the switch, the fetch must target the second ref.
		await waitFor(() =>
			expect(getDiff).toHaveBeenLastCalledWith(
				"test-chat",
				expect.objectContaining({
					remote_origin: "https://github.com/coder/coder",
					git_branch: "fix/second",
				}),
			),
		);
	});

	it("fetches a branch-only ref's diff even without a PR URL", async () => {
		const getDiff = vi
			.spyOn(API.experimental, "getChatDiffContents")
			.mockResolvedValue(diffContents("test-chat"));

		renderPanel({
			remoteDiffStats: [
				{
					...MockChatDiffStatus,
					git_branch: "feature/no-pr-yet",
					url: undefined,
					pr_number: undefined,
					pull_request_state: undefined,
					pull_request_title: "",
				},
			],
		});

		// The branch has no PR URL, but its ref selector must still
		// drive a diff fetch.
		await waitFor(() =>
			expect(getDiff).toHaveBeenCalledWith(
				"test-chat",
				expect.objectContaining({
					remote_origin: "https://github.com/coder/coder",
					git_branch: "feature/no-pr-yet",
				}),
			),
		);
	});

	it("adopts the first refs when they arrive after mount", async () => {
		const getDiff = vi
			.spyOn(API.experimental, "getChatDiffContents")
			.mockResolvedValue(diffContents("test-chat"));

		const view = renderPanel({ remoteDiffStats: undefined });

		const firstRef = {
			...MockChatDiffStatus,
			pull_request_title: "feat: first change",
			git_branch: "feat/first",
			pr_number: 23020,
			url: "https://github.com/coder/coder/pull/23020",
		};
		const secondRef = {
			...MockChatDiffStatus,
			pull_request_title: "fix: second change",
			git_branch: "fix/second",
			pr_number: 23021,
			url: "https://github.com/coder/coder/pull/23021",
		};
		view.rerender(
			<Wrapper>
				<GitPanel
					chatId="test-chat"
					onRefresh={() => true}
					onCommit={() => {}}
					repositories={new Map()}
					remoteDiffStats={[firstRef, secondRef]}
				/>
			</Wrapper>,
		);

		// The first arriving ref drives the default fetch.
		await waitFor(() =>
			expect(getDiff).toHaveBeenCalledWith(
				"test-chat",
				expect.objectContaining({
					remote_origin: "https://github.com/coder/coder",
					git_branch: "feat/first",
				}),
			),
		);
	});

	it("shows the selected PR's title when the primary is a branch", async () => {
		const user = userEvent.setup();

		renderPanel({
			remoteDiffStats: [
				{
					...MockChatDiffStatus,
					git_branch: "feature/no-pr-yet",
					url: undefined,
					pr_number: undefined,
					pull_request_state: undefined,
					pull_request_title: "",
				},
				{
					...MockChatDiffStatus,
					pull_request_title: "fix: second change",
					git_branch: "fix/second",
					pr_number: 23021,
					url: "https://github.com/coder/coder/pull/23021",
				},
			],
		});

		await user.click(screen.getByRole("button", { name: "Switch git view" }));
		const menu = await screen.findByRole("menu");
		await user.click(within(menu).getByText("PR #23021"));

		// The title row must carry the selected PR's title, not the
		// branch-only primary's.
		screen.getByText("fix: second change");
	});

	it("settles without looping when a PR is tracked but no ref has data", () => {
		// A chat can know its PR number before any ref status row
		// exists. The view reconcile is derived in render, so this
		// state must settle instead of re-setting the view forever.
		expect(() =>
			renderPanel({
				prTab: { prNumber: 23020, chatId: "test-chat" },
				remoteDiffStats: undefined,
			}),
		).not.toThrow();
	});

	it("does not offer a non-primary keyless ref", async () => {
		const user = userEvent.setup();

		// A chat upgraded from the unkeyed schema keeps its legacy row
		// without origin and branch, behind the keyed refs that the
		// agent reported later.
		renderPanel({
			remoteDiffStats: [
				{
					...MockChatDiffStatus,
					pull_request_title: "fix: keyed change",
					git_branch: "fix/keyed",
					pr_number: 23021,
					url: "https://github.com/coder/coder/pull/23021",
				},
				{
					...MockChatDiffStatus,
					pull_request_title: "feat: second keyed change",
					git_branch: "feat/second-keyed",
					pr_number: 23022,
					url: "https://github.com/coder/coder/pull/23022",
				},
				{
					...MockChatDiffStatus,
					remote_origin: "",
					git_branch: "",
					pull_request_title: "fix: legacy change",
					pr_number: 23020,
					url: "https://github.com/coder/coder/pull/23020",
				},
			],
		});

		await user.click(screen.getByRole("button", { name: "Switch git view" }));
		const menu = await screen.findByRole("menu");

		// The keyed refs stay selectable. Selecting one still drives
		// the ref-specific fetch, which the first test covers.
		await within(menu).findByRole("menuitem", { name: /fix: keyed change/ });
		await within(menu).findByRole("menuitem", {
			name: /feat: second keyed change/,
		});

		// The legacy row must not be offered: selecting it would send
		// an empty selector, which the API resolves to the primary.
		expect(
			within(menu).queryByRole("menuitem", { name: /fix: legacy change/ }),
		).not.toBeInTheDocument();
	});
});
