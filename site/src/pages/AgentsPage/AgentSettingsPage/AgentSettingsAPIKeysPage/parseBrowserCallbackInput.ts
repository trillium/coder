export const BROWSER_CALLBACK_EMPTY_ERROR =
	"Paste the callback first. After approving, copy the full address of the page that fails to load and paste it here.";
export const BROWSER_CALLBACK_MALFORMED_ERROR =
	"That does not look like the callback. Copy the full address-bar URL of the failed localhost:1455 page (it contains code=...) or paste just the code.";

const BROWSER_BARE_CODE_PATTERN = /^[A-Za-z0-9._~+=-]+$/;

/**
 * Validates a pasted browser sign-in callback: the full localhost callback
 * URL, a bare code, or `code#state`. The server repeats the same parse; this
 * one only rejects empty or malformed input before the exchange request.
 */
export const parseBrowserCallbackInput = (
	raw: string,
): { ok: true; input: string } | { ok: false; error: string } => {
	const trimmed = raw.trim();
	if (!trimmed) {
		return { ok: false, error: BROWSER_CALLBACK_EMPTY_ERROR };
	}
	if (trimmed.includes("code=")) {
		const codeParam = trimmed.match(/(?:[?&#]|^)code=([^&#\s]*)/);
		if (codeParam && codeParam[1].length > 0) {
			return { ok: true, input: trimmed };
		}
		return { ok: false, error: BROWSER_CALLBACK_MALFORMED_ERROR };
	}
	// The server splits `code#state` on the first "#".
	if (trimmed.includes("#")) {
		const before = trimmed.slice(0, trimmed.indexOf("#"));
		if (before.length >= 4 && BROWSER_BARE_CODE_PATTERN.test(before)) {
			return { ok: true, input: trimmed };
		}
		return { ok: false, error: BROWSER_CALLBACK_MALFORMED_ERROR };
	}
	// The bare-code pattern allows "=", so reject query-shaped input and a
	// "localhost..." paste that lost its code parameter before matching it.
	if (trimmed.includes("=") || trimmed.startsWith("localhost")) {
		return { ok: false, error: BROWSER_CALLBACK_MALFORMED_ERROR };
	}
	if (trimmed.length >= 4 && BROWSER_BARE_CODE_PATTERN.test(trimmed)) {
		return { ok: true, input: trimmed };
	}
	return { ok: false, error: BROWSER_CALLBACK_MALFORMED_ERROR };
};
