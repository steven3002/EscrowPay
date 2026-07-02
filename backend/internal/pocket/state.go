package pocket

// State is a Pocket's position in the escrow lifecycle. The set is closed and
// normative; see project-flow §4.
type State string

const (
	StateCreated          State = "CREATED"
	StateFunded           State = "FUNDED"
	StateDeliveredPending State = "DELIVERED_PENDING"
	StateSettled          State = "SETTLED"
	StateDisputed         State = "DISPUTED"
	StateFrozen           State = "FROZEN"
	StateRefunded         State = "REFUNDED"
	StateCancelled        State = "CANCELLED"
	StateExpired          State = "EXPIRED"
)

// IsTerminal reports whether the state admits no further transitions. Terminal
// immutability is enforced once, centrally, in Transition.
func (s State) IsTerminal() bool {
	switch s {
	case StateSettled, StateRefunded, StateCancelled, StateExpired:
		return true
	default:
		return false
	}
}

// Structure distinguishes a two-party pocket from a three-party brokered one.
// It changes acceptance guards and settlement-leg count only — never the set of
// states or transitions.
type Structure string

const (
	StructureP2P      Structure = "p2p"
	StructureBrokered Structure = "brokered"
)

// Mode selects the protection the buyer receives. Instant is delivery-only
// (zero inspection window); Cooldown adds a quality-inspection window.
type Mode string

const (
	ModeInstant  Mode = "instant"
	ModeCooldown Mode = "cooldown"
)

// Role identifies a participant within a single pocket. Roles are per-pocket,
// not global attributes of a user.
type Role string

const (
	RoleBuyer  Role = "buyer"
	RoleVendor Role = "vendor"
	RoleBroker Role = "broker"
)

// DisputeClass records which burden of proof applies once a pocket is disputed.
type DisputeClass string

const (
	DisputeNone           DisputeClass = ""
	DisputeNotDelivered   DisputeClass = "not_delivered"
	DisputeNotAsDescribed DisputeClass = "not_as_described"
)

// SanctionKind is an enforcement action attached to a settlement outcome.
type SanctionKind string

const (
	SanctionFraudFlag SanctionKind = "fraud_flag"
	SanctionStrike    SanctionKind = "strike"
)
