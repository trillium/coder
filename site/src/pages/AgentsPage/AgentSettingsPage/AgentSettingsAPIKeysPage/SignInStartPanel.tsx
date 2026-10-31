import { Badge } from "#/components/Badge/Badge";
import { Button } from "#/components/Button/Button";
import { Spinner } from "#/components/Spinner/Spinner";

type SignInStartPanelProps = {
	label: string;
	badge: string;
	badgeVariant: "default" | "green";
	description: string;
	isPending: boolean;
	onStart: () => void;
};

/** Idle state of a provider sign-in flow: the start button and its help text. */
export const SignInStartPanel: React.FC<SignInStartPanelProps> = ({
	label,
	badge,
	badgeVariant,
	description,
	isPending,
	onStart,
}) => {
	return (
		<div className="mt-6 flex flex-col gap-2 border-t border-solid border-border pt-6">
			<div className="flex flex-col gap-3 sm:flex-row sm:items-center">
				<Button
					type="button"
					variant="outline"
					size="sm"
					onClick={onStart}
					disabled={isPending}
				>
					<Spinner loading={isPending} />
					{label}
				</Button>
				<Badge size="sm" variant={badgeVariant}>
					{badge}
				</Badge>
			</div>
			<p className="m-0 text-sm text-content-secondary">{description}</p>
		</div>
	);
};
