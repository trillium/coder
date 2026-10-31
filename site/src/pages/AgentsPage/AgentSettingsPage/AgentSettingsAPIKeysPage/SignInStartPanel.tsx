import { Badge } from "#/components/Badge/Badge";
import { Button } from "#/components/Button/Button";
import { Spinner } from "#/components/Spinner/Spinner";
import { SignInSection } from "./SignInSection";

type SignInStartPanelProps = {
	label: string;
	badge: string;
	badgeVariant: "default" | "green";
	description: string;
	isPending: boolean;
	/** Focuses the start button, used when a user-ended flow returns here. */
	autoFocus: boolean;
	onStart: () => void;
};

/** Idle state of a provider sign-in flow: the start button and its help text. */
export const SignInStartPanel: React.FC<SignInStartPanelProps> = ({
	label,
	badge,
	badgeVariant,
	description,
	isPending,
	autoFocus,
	onStart,
}) => {
	return (
		<SignInSection className="gap-2">
			<div className="flex flex-col gap-3 sm:flex-row sm:items-center">
				<Button
					type="button"
					variant="outline"
					size="sm"
					onClick={onStart}
					disabled={isPending}
					autoFocus={autoFocus}
				>
					<Spinner loading={isPending} />
					{label}
				</Button>
				<Badge size="sm" variant={badgeVariant}>
					{badge}
				</Badge>
			</div>
			<p className="m-0 text-sm text-content-secondary">{description}</p>
		</SignInSection>
	);
};
