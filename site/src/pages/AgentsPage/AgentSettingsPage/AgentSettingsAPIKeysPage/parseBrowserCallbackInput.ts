export const BROWSER_CALLBACK_EMPTY_ERROR =
	"Paste the callback first. After approving, copy the full address of the page that fails to load and paste it here.";
export const BROWSER_CALLBACK_MALFORMED_ERROR =
	"That does not look like the callback. Copy the full address-bar URL of the failed localhost:1455 page (it contains code=...) or paste just the code.";

const BROWSER_BARE_CODE_PATTERN = /^[A-Za-z0-9._~+=-]+$/;

/**
 * Validates a pasted browser sign-in callback: the full localhost callback
 * URL, a bare query string, `code#state`, or a bare code. It checks in the
 * server's order so input that passes here yields a non-empty code there.
 * The server does the authoritative parse; this only rejects empty or
 * malformed input before the exchange request.
 */
export const parseBrowserCallbackInput = (
	raw: string,
): { ok: true; input: string } | { ok: false; error: string } => {
	const trimmed = raw.trim();
	if (!trimmed) {
		return { ok: false, error: BROWSER_CALLBACK_EMPTY_ERROR };
	}
	const valid = { ok: true, input: trimmed } as const;
	const malformed = {
		ok: false,
		error: BROWSER_CALLBACK_MALFORMED_ERROR,
	} as const;
	if (URL.canParse(trimmed)) {
		const url = new URL(trimmed);
		if (url.host) {
			return url.searchParams.get("code") ? valid : malformed;
		}
	}
	if (trimmed.includes("#")) {
		const code = trimmed.slice(0, trimmed.indexOf("#"));
		return code.length >= 4 && BROWSER_BARE_CODE_PATTERN.test(code)
			? valid
			: malformed;
	}
	if (trimmed.includes("code=")) {
		return new URLSearchParams(trimmed).get("code") ? valid : malformed;
	}
	// The bare-code pattern allows "=", so reject query-shaped input and a
	// "localhost..." paste that lost its scheme before matching it.
	if (trimmed.includes("=") || trimmed.startsWith("localhost")) {
		return malformed;
	}
	return trimmed.length >= 4 && BROWSER_BARE_CODE_PATTERN.test(trimmed)
		? valid
		: malformed;
};
