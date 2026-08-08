package topics

// A plain exported constant.
const OrderCreated = "orders.created"

// A constant assembled from other constants.
const prefix = "acme."
const UserCreated = prefix + "users.created"

// A package-level var holding a string.
var PaymentTaken = "acme.payments.taken"

// A field on a package-level struct instance.
type Registry struct {
	Shipped string
	Refund  string
}

var Topics = Registry{
	Shipped: "acme.orders.shipped",
	Refund:  "acme.orders.refund",
}

// An anonymous struct, the other common shape.
var Anon = struct{ Cancelled string }{Cancelled: "acme.orders.cancelled"}
