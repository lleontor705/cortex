package ast

// CapabilityLevel declares extraction fidelity per fact class; L0 is the zero
// capability: no eligible facts may be emitted for that class.
type CapabilityLevel int

const (
	CapabilityL0 CapabilityLevel = iota
	CapabilityL1
	CapabilityL2
	CapabilityL3
)

var capabilityNames = [...]string{"L0", "L1", "L2", "L3"}

func (l CapabilityLevel) String() string {
	if l >= CapabilityL0 && l <= CapabilityL3 {
		return capabilityNames[l]
	}
	return "L?"
}

var capabilityByToken = map[string]CapabilityLevel{"L0": CapabilityL0, "L1": CapabilityL1, "L2": CapabilityL2, "L3": CapabilityL3}

func ParseCapabilityLevel(s string) (CapabilityLevel, error) {
	if l, ok := capabilityByToken[s]; ok {
		return l, nil
	}
	return CapabilityL0, ErrASTIRInvalid
}

// Capabilities is the per-language contract a v2 adapter declares up front.
type Capabilities struct {
	Language     string
	Declarations CapabilityLevel
	References   CapabilityLevel
}

func (c Capabilities) Validate() error {
	if c.Language == "" || c.Declarations < CapabilityL0 || c.Declarations > CapabilityL3 ||
		c.References < CapabilityL0 || c.References > CapabilityL3 {
		return ErrASTIRInvalid
	}
	return nil
}

// CheckEmitted rejects L0 classes that still emitted facts: an overclaim.
func (c Capabilities) CheckEmitted(decls, refs int) error {
	if c.Declarations == CapabilityL0 && decls > 0 {
		return ErrASTCapabilityOverclaim
	}
	if c.References == CapabilityL0 && refs > 0 {
		return ErrASTCapabilityOverclaim
	}
	return nil
}
