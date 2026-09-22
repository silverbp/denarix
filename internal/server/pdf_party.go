// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package server

import (
	"strings"

	"github.com/silverbp/denarix/internal/db/sqlcgen"
	"github.com/silverbp/denarix/internal/pdf"
)

// PDF address blocks for the business issuing a document and the
// contact being billed - shared by GetInvoicePdf and GetEstimatePdf.

// businessParty builds the PDF party for the business issuing a document:
// name and mailing address in Lines (window-safe — see
// pdf.WindowEnvelopeHeader), phone and email in Contact (shown in the page
// footer, never inside a window).
func businessParty(b sqlcgen.Business) pdf.Party {
	return pdf.Party{
		Name:    b.Name,
		Lines:   formatAddressLines(b.AddressLine1, b.AddressLine2, b.City, b.State, b.PostalCode),
		Contact: contactLines(b.Phone, b.Email),
	}
}

// billToParty builds the PDF party for the contact being billed: name and
// billing address in Lines (window-safe), phone and email in Contact.
func billToParty(c sqlcgen.Contact) pdf.Party {
	return pdf.Party{
		Name:    c.Name,
		Lines:   formatAddressLines(c.BillingAddressLine1, c.BillingAddressLine2, c.BillingCity, c.BillingState, c.BillingPostalCode),
		Contact: contactLines(c.Phone, c.Email),
	}
}

// contactLines collects whichever of phone/email are set into a Party's
// Contact slice, in that order.
func contactLines(phone, email *string) []string {
	var lines []string
	if v := derefOr(phone, ""); v != "" {
		lines = append(lines, v)
	}
	if v := derefOr(email, ""); v != "" {
		lines = append(lines, v)
	}
	return lines
}

// formatAddressLines renders a street address as "line1", "line2" (if
// present), and "City, State PostalCode" — omitting any piece that's unset
// rather than leaving stray commas or blank lines. line1/line2 are used
// verbatim, one PDF line each — bad data (e.g. a migrated contact whose
// billing_address_line1 crams in more than a street address) needs
// cleaning up at the contact record itself, not parsed back apart here.
func formatAddressLines(line1, line2, city, state, postal *string) []string {
	var lines []string
	if v := strings.TrimSpace(derefOr(line1, "")); v != "" {
		lines = append(lines, v)
	}
	if v := strings.TrimSpace(derefOr(line2, "")); v != "" {
		lines = append(lines, v)
	}
	c, stateZip := derefOr(city, ""), strings.TrimSpace(derefOr(state, "")+" "+derefOr(postal, ""))
	var cityLine string
	switch {
	case c != "" && stateZip != "":
		cityLine = c + ", " + stateZip
	case c != "":
		cityLine = c
	default:
		cityLine = stateZip
	}
	if cityLine != "" {
		lines = append(lines, cityLine)
	}
	return lines
}
