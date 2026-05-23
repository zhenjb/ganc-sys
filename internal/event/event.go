package event

const TypeDeposit = "ob.zkdex.v1.EventDeposit"

type Event struct {
	Type       string            `json:"type"`
	Attributes map[string]string `json:"attributes"`
}
