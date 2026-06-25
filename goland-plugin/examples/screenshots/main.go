// Package main is a self-contained sample for taking Unfold plugin screenshots.
//
// Open this folder in GoLand with the Unfold plugin installed, then follow the
// recipes in README.md — each points at a call to expand (Ctrl+Alt+U) that
// shows off a specific feature: nested expansion, in-frame folding, the
// recursion badge, and the interface-implementation picker.
package main

import (
	"errors"
	"fmt"
)

func main() {
	order := Order{
		ID:     "A-1042",
		Region: "CA",
		Items: []Item{
			{Name: "Widget", Qty: 3, PriceCents: 1299},
			{Name: "Gadget", Qty: 1, PriceCents: 4999},
		},
	}

	// SCREENSHOT 1 (hero): caret on processOrder, press Ctrl+Alt+U, then expand
	// the calls inside the frame to show nesting.
	receipt := processOrder(order, CreditCard{Number: "4111111111111111", CVV: "123"})
	fmt.Println(receipt)
}

// processOrder runs the full pipeline for one order: validate, total, charge,
// then format a receipt. Expand it for the hero shot, then expand the calls
// inside the frame (validate, orderTotal, pay.Charge) to show nesting.
func processOrder(o Order, pay PaymentMethod) Receipt {
	if err := validate(o); err != nil {
		return Receipt{OrderID: o.ID, Err: err.Error()}
	}
	total := orderTotal(o)
	auth := pay.Charge(total)
	return formatReceipt(o, total, auth)
}

// validate has a few foldable blocks — expand it, then use the gutter fold
// arrows inside the frame to demonstrate in-frame folding.
func validate(o Order) error {
	if o.ID == "" {
		return errors.New("order has no ID")
	}
	if len(o.Items) == 0 {
		return errors.New("order has no items")
	}
	for _, item := range o.Items {
		if item.Qty <= 0 {
			return fmt.Errorf("item %q has non-positive quantity", item.Name)
		}
		if item.PriceCents < 0 {
			return fmt.Errorf("item %q has negative price", item.Name)
		}
	}
	return nil
}

// orderTotal sums the line items and adds regional tax.
func orderTotal(o Order) int {
	sub := 0
	for _, item := range o.Items {
		sub += item.Qty * item.PriceCents
	}
	return applyTax(sub, taxRate(o.Region))
}

func applyTax(cents int, rate float64) int {
	return cents + int(float64(cents)*rate)
}

// taxRate is a tidy switch — compact and readable when spliced inline.
func taxRate(region string) float64 {
	switch region {
	case "CA":
		return 0.13
	case "US":
		return 0.07
	case "EU":
		return 0.20
	default:
		return 0.0
	}
}

func formatReceipt(o Order, total int, auth string) Receipt {
	return Receipt{
		OrderID: o.ID,
		Total:   total,
		Auth:    auth,
	}
}

// quantityOf walks a (possibly nested) bundle tree and sums leaf quantities.
// SCREENSHOT (recursion badge): expand quantityOf, then expand the recursive
// quantityOf(sub) call inside the frame to trigger the "↻ recursive" badge.
func quantityOf(b Bundle) int {
	n := 0
	for _, item := range b.Items {
		n += item.Qty
	}
	for _, sub := range b.Bundles {
		n += quantityOf(sub)
	}
	return n
}

// --- domain types ---

type Item struct {
	Name       string
	Qty        int
	PriceCents int
}

type Order struct {
	ID     string
	Region string
	Items  []Item
}

type Bundle struct {
	Items   []Item
	Bundles []Bundle
}

type Receipt struct {
	OrderID string
	Total   int
	Auth    string
	Err     string
}

func (r Receipt) String() string {
	if r.Err != "" {
		return fmt.Sprintf("order %s failed: %s", r.OrderID, r.Err)
	}
	return fmt.Sprintf("order %s — total %d¢ — auth %s", r.OrderID, r.Total, r.Auth)
}
