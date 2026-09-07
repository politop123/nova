package policy

type Permission string

const (
	Read    Permission = "READ"
	Write   Permission = "WRITE"
	Confirm Permission = "CONFIRM"
)

type Context struct {
	UserID        string
	ToolName      string
	Permission    Permission
	Authenticated bool
	ToolEnabled   bool
}

type Outcome string

const (
	Allow       Outcome = "allow"
	NeedConfirm Outcome = "confirm"
	Deny        Outcome = "deny"
)

type Decision struct {
	Outcome Outcome `json:"outcome"`
	Reason  string  `json:"reason"`
}

func Evaluate(ctx Context) Decision {
	if !ctx.Authenticated {
		return Decision{Outcome: Deny, Reason: "the user is not authenticated"}
	}
	if !ctx.ToolEnabled {
		return Decision{Outcome: Deny, Reason: "the tool is disabled"}
	}
	if ctx.Permission == Confirm {
		return Decision{Outcome: NeedConfirm, Reason: "explicit approval is required for this exact action"}
	}
	return Decision{Outcome: Allow, Reason: string(ctx.Permission) + " action is permitted"}
}
