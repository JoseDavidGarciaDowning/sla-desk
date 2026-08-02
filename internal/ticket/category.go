package ticket

// Category is what a ticket is about, as defined in docs/spec.md §4.7.
//
// A fixed set rather than free text: the agent dashboard filters and groups by
// it, and grouping over free text stops working the moment "Billing", "billing"
// and "facturación" coexist. Widening the set means one migration and one
// constant here.
type Category string

const (
	CategoryBilling   Category = "billing"
	CategoryTechnical Category = "technical"
	CategoryAccount   Category = "account"
	CategoryOther     Category = "other"
)
