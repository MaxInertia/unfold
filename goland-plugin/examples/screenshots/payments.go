package main

import "fmt"

// PaymentMethod is satisfied by several concrete methods. Because the call
// `pay.Charge(total)` in processOrder dispatches through this interface,
// expanding it pops the implementation picker (CreditCard / PayPal / Cash) —
// pick one to splice that implementation inline.
type PaymentMethod interface {
	Charge(cents int) string
}

type CreditCard struct {
	Number string
	CVV    string
}

func (c CreditCard) Charge(cents int) string {
	last4 := c.Number
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	return fmt.Sprintf("cc-****%s-%d", last4, cents)
}

type PayPal struct {
	Email string
}

func (p PayPal) Charge(cents int) string {
	return fmt.Sprintf("pp-%s-%d", p.Email, cents)
}

type Cash struct{}

func (Cash) Charge(cents int) string {
	return fmt.Sprintf("cash-%d", cents)
}
