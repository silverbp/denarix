// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	typepb "google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/protobuf/proto"

	denarixv1 "github.com/silverbp/denarix/gen/denarix/v1"
	"github.com/silverbp/denarix/internal/dxctl/output"
	"github.com/silverbp/denarix/internal/dxctl/resource"
)

// Every report command has the same shape: parse its date flags, dial, call
// either the PDF RPC (-o pdf) or the structured one, then print the result
// as a hand-laid-out table (reports aren't flat rows, so they don't use
// resource.Column) or as json/yaml. reportSpec captures what differs per
// report; newReportSubCmd does the rest once.

// reportDates carries whichever date flags a report declared.
type reportDates struct {
	asOf, start, end *typepb.Date
}

type reportSpec struct {
	use, summary, example string
	// asOf declares --as-of (default today).
	asOf bool
	// period declares --start/--end; end defaults to today, start to
	// startDefault - or is required when startDefault is "".
	period       bool
	startDefault string
	// endHelp overrides the --end usage text when the report needs to say
	// more than "period end date".
	endHelp string
	// flags registers the report's own scope flags (--account, --contact).
	flags func(cmd *cobra.Command)
	pdf   func(r run, d reportDates) ([]byte, error)
	fetch func(r run, d reportDates) (proto.Message, error)
	print func(w io.Writer, m proto.Message)
}

func newReportCmd() *cobra.Command {
	root := &cobra.Command{Use: "report", Short: "Run a financial report"}
	root.AddCommand(
		newReportSubCmd(trialBalanceReport()),
		newReportSubCmd(balanceSheetReport()),
		newReportSubCmd(incomeStatementReport()),
		newReportSubCmd(generalLedgerReport()),
		newReportSubCmd(customerStatementReport()),
	)
	return root
}

func newReportSubCmd(spec reportSpec) *cobra.Command {
	var asOf, start, end string
	cmd := &cobra.Command{
		Use:  spec.use,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			var d reportDates
			var err error
			if spec.asOf {
				if d.asOf, err = parseDateFlag("as-of", asOf); err != nil {
					return err
				}
			}
			if spec.period {
				if d.start, err = parseDateFlag("start", start); err != nil {
					return err
				}
				if d.end, err = parseDateFlag("end", end); err != nil {
					return err
				}
			}
			r, err := dialRun(cmd)
			if err != nil {
				return err
			}
			defer r.conn.Close()

			w := cmd.OutOrStdout()
			if flagOutput == output.FormatPDF {
				content, err := spec.pdf(r, d)
				if err != nil {
					return err
				}
				_, err = w.Write(content)
				return err
			}
			m, err := spec.fetch(r, d)
			if err != nil {
				return err
			}
			if flagOutput != output.FormatTable {
				return output.PrintOne(w, flagOutput, m, nil)
			}
			spec.print(w, m)
			return nil
		},
	}
	today := time.Now().Format("2006-01-02")
	if spec.asOf {
		cmd.Flags().StringVar(&asOf, "as-of", today, "as-of date, YYYY-MM-DD (default today)")
	}
	if spec.period {
		if spec.startDefault == "" {
			cmd.Flags().StringVar(&start, "start", "", "period start date, YYYY-MM-DD (required)")
			_ = cmd.MarkFlagRequired("start")
		} else {
			cmd.Flags().StringVar(&start, "start", spec.startDefault, "period start date, YYYY-MM-DD (default: inception)")
		}
		endHelp := spec.endHelp
		if endHelp == "" {
			endHelp = "period end date, YYYY-MM-DD (default today)"
		}
		cmd.Flags().StringVar(&end, "end", today, endHelp)
	}
	if spec.flags != nil {
		spec.flags(cmd)
	}
	resource.Doc{
		Summary:  spec.summary,
		Detail:   "Supports -o pdf to render as PDF, written to stdout instead of table/json/yaml.",
		Examples: []resource.Example{{Cmd: spec.example}},
	}.Apply(cmd)
	return cmd
}

// typedPrinter adapts a printer for one report message to reportSpec.print.
func typedPrinter[T proto.Message](f func(w io.Writer, m T)) func(io.Writer, proto.Message) {
	return func(w io.Writer, m proto.Message) { f(w, m.(T)) }
}

func reportClient(r run) denarixv1.ReportingServiceClient {
	return denarixv1.NewReportingServiceClient(r.conn)
}

func trialBalanceReport() reportSpec {
	return reportSpec{
		use: "trial-balance", summary: "Trial balance as of a date", example: "dxctl report trial-balance --as-of 2026-01-31",
		asOf: true,
		pdf: func(r run, d reportDates) ([]byte, error) {
			resp, err := reportClient(r).GetTrialBalancePdf(r.ctx, &denarixv1.GetTrialBalancePdfRequest{BusinessId: r.businessID, AsOf: d.asOf})
			return resp.GetContent(), err
		},
		fetch: func(r run, d reportDates) (proto.Message, error) {
			resp, err := reportClient(r).GetTrialBalance(r.ctx, &denarixv1.GetTrialBalanceRequest{BusinessId: r.businessID, AsOf: d.asOf})
			return resp.GetTrialBalance(), err
		},
		print: typedPrinter(func(w io.Writer, tb *denarixv1.TrialBalance) {
			fmt.Fprintln(w, "CODE\tNAME\tDEBIT\tCREDIT")
			for _, l := range tb.GetLines() {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", l.GetAccountCode(), l.GetAccountName(), l.GetDebit().GetValue(), l.GetCredit().GetValue())
			}
			fmt.Fprintf(w, "TOTAL\t\t%s\t%s\n", tb.GetTotalDebit().GetValue(), tb.GetTotalCredit().GetValue())
		}),
	}
}

func balanceSheetReport() reportSpec {
	return reportSpec{
		use: "balance-sheet", summary: "Balance sheet as of a date", example: "dxctl report balance-sheet --as-of 2026-01-31",
		asOf: true,
		pdf: func(r run, d reportDates) ([]byte, error) {
			resp, err := reportClient(r).GetBalanceSheetPdf(r.ctx, &denarixv1.GetBalanceSheetPdfRequest{BusinessId: r.businessID, AsOf: d.asOf})
			return resp.GetContent(), err
		},
		fetch: func(r run, d reportDates) (proto.Message, error) {
			resp, err := reportClient(r).GetBalanceSheet(r.ctx, &denarixv1.GetBalanceSheetRequest{BusinessId: r.businessID, AsOf: d.asOf})
			return resp.GetBalanceSheet(), err
		},
		print: typedPrinter(func(w io.Writer, bs *denarixv1.BalanceSheet) {
			fmt.Fprintln(w, "SECTION\tACCOUNT\tASSET\tLIABILITY")
			for i, s := range bs.GetSections() {
				for _, l := range s.GetAssetLines() {
					fmt.Fprintf(w, "%s\t%s\t%s\t\n", s.GetTitle(), l.GetAccountName(), l.GetBalance().GetValue())
				}
				for _, l := range s.GetLiabilityLines() {
					fmt.Fprintf(w, "%s\t%s\t\t%s\n", s.GetTitle(), l.GetAccountName(), l.GetBalance().GetValue())
				}
				fmt.Fprintf(w, "%s\t(total)\t%s\t%s\n", s.GetTitle(), s.GetTotalAssets().GetValue(), s.GetTotalLiabilities().GetValue())
				switch i {
				case 1:
					fmt.Fprintf(w, "\tNet current assets (liabilities)\t%s\t\n", bs.GetNetCurrentAssets().GetValue())
					fmt.Fprintf(w, "\tTotal assets less current liabilities\t%s\t\n", bs.GetTotalAssetsLessCurrentLiabilities().GetValue())
				case 2:
					fmt.Fprintf(w, "\tTotal net assets (liabilities)\t%s\t\n", bs.GetTotalNetAssets().GetValue())
				}
			}
			fmt.Fprintf(w, "TOTAL\t\t%s\t%s\n", bs.GetTotalAssets().GetValue(), bs.GetTotalLiabilities().GetValue())
		}),
	}
}

func incomeStatementReport() reportSpec {
	return reportSpec{
		use: "income-statement", summary: "Income statement (P&L) over a date range", example: "dxctl report income-statement --start 2026-01-01 --end 2026-01-31",
		period: true,
		pdf: func(r run, d reportDates) ([]byte, error) {
			resp, err := reportClient(r).GetIncomeStatementPdf(r.ctx, &denarixv1.GetIncomeStatementPdfRequest{BusinessId: r.businessID, PeriodStart: d.start, PeriodEnd: d.end})
			return resp.GetContent(), err
		},
		fetch: func(r run, d reportDates) (proto.Message, error) {
			resp, err := reportClient(r).GetIncomeStatement(r.ctx, &denarixv1.GetIncomeStatementRequest{BusinessId: r.businessID, PeriodStart: d.start, PeriodEnd: d.end})
			return resp.GetIncomeStatement(), err
		},
		print: typedPrinter(func(w io.Writer, is *denarixv1.IncomeStatement) {
			section := func(title string, lines []*denarixv1.IncomeStatementLine, totalLabel string, total *denarixv1.Decimal) {
				fmt.Fprintln(w, title)
				for _, l := range lines {
					fmt.Fprintf(w, "  %s\t%s\t%s\n", l.GetAccountCode(), l.GetAccountName(), l.GetAmount().GetValue())
				}
				fmt.Fprintf(w, "  %s\t\t%s\n", totalLabel, total.GetValue())
			}
			section("REVENUE", is.GetRevenue(), "TOTAL REVENUE", is.GetTotalRevenue())
			section("COST OF GOODS SOLD", is.GetCostOfGoodsSold(), "TOTAL COST OF GOODS SOLD", is.GetTotalCostOfGoodsSold())
			fmt.Fprintf(w, "GROSS PROFIT\t\t%s\n", is.GetGrossProfit().GetValue())
			section("OPERATING EXPENSES", is.GetOperatingExpenses(), "TOTAL OPERATING EXPENSES", is.GetTotalOperatingExpenses())
			fmt.Fprintf(w, "TOTAL EXPENSES\t\t%s\n", is.GetTotalExpenses().GetValue())
			fmt.Fprintf(w, "NET INCOME\t\t%s\n", is.GetNetIncome().GetValue())
		}),
	}
}

func generalLedgerReport() reportSpec {
	var account int32
	return reportSpec{
		use: "general-ledger", summary: "General-ledger detail for one account over a date range", example: "dxctl report general-ledger --account 10 --start 2026-01-01 --end 2026-01-31",
		period: true, startDefault: "0001-01-01",
		flags: func(cmd *cobra.Command) {
			cmd.Flags().Int32Var(&account, "account", 0, "ledger account id (required)")
			_ = cmd.MarkFlagRequired("account")
		},
		pdf: func(r run, d reportDates) ([]byte, error) {
			resp, err := reportClient(r).GetGeneralLedgerPdf(r.ctx, &denarixv1.GetGeneralLedgerPdfRequest{BusinessId: r.businessID, AccountId: account, PeriodStart: d.start, PeriodEnd: d.end})
			return resp.GetContent(), err
		},
		fetch: func(r run, d reportDates) (proto.Message, error) {
			resp, err := reportClient(r).GetGeneralLedger(r.ctx, &denarixv1.GetGeneralLedgerRequest{BusinessId: r.businessID, AccountId: account, PeriodStart: d.start, PeriodEnd: d.end})
			return resp.GetGeneralLedger(), err
		},
		print: typedPrinter(func(w io.Writer, gl *denarixv1.GeneralLedger) {
			fmt.Fprintf(w, "%s %s\n", gl.GetAccountCode(), gl.GetAccountName())
			fmt.Fprintln(w, "DATE\tTXN\tDEBIT\tCREDIT\tBALANCE")
			for _, l := range gl.GetLines() {
				fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\n", resource.FormatDate(l.GetTransactionDate()),
					l.GetLedgerTransactionId(), l.GetDebit().GetValue(), l.GetCredit().GetValue(), l.GetRunningBalance().GetValue())
			}
			fmt.Fprintf(w, "ENDING BALANCE\t\t\t\t%s\n", gl.GetEndingBalance().GetValue())
		}),
	}
}

func customerStatementReport() reportSpec {
	var contact int64
	return reportSpec{
		use: "customer-statement", summary: "Invoice/payment activity, running balance, and aging for one contact", example: "dxctl report customer-statement --contact 5 --start 2026-01-01 --end 2026-01-31",
		period: true, startDefault: "0001-01-01",
		endHelp: "activity period end date, YYYY-MM-DD (default today); aging is always as of this date",
		flags: func(cmd *cobra.Command) {
			cmd.Flags().Int64Var(&contact, "contact", 0, "contact id (required)")
			_ = cmd.MarkFlagRequired("contact")
		},
		pdf: func(r run, d reportDates) ([]byte, error) {
			resp, err := reportClient(r).GetCustomerStatementPdf(r.ctx, &denarixv1.GetCustomerStatementPdfRequest{ContactId: contact, PeriodStart: d.start, PeriodEnd: d.end})
			return resp.GetContent(), err
		},
		fetch: func(r run, d reportDates) (proto.Message, error) {
			resp, err := reportClient(r).GetCustomerStatement(r.ctx, &denarixv1.GetCustomerStatementRequest{ContactId: contact, PeriodStart: d.start, PeriodEnd: d.end})
			return resp.GetStatement(), err
		},
		print: typedPrinter(func(w io.Writer, st *denarixv1.CustomerStatement) {
			fmt.Fprintf(w, "%s\n", st.GetContactName())
			fmt.Fprintln(w, "DATE\tDESCRIPTION\tDEBIT\tCREDIT\tBALANCE")
			for _, a := range st.GetActivity() {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", resource.FormatDate(a.GetDate()),
					a.GetDescription(), a.GetDebit().GetValue(), a.GetCredit().GetValue(), a.GetRunningBalance().GetValue())
			}
			fmt.Fprintf(w, "ENDING BALANCE\t\t\t\t%s\n", st.GetEndingBalance().GetValue())
			fmt.Fprintln(w)
			fmt.Fprintln(w, "AGING\tCURRENT\t1-30\t31-60\t61-90\t90+")
			amounts := make([]any, 0, len(st.GetAgingBuckets()))
			for _, b := range st.GetAgingBuckets() {
				amounts = append(amounts, b.GetAmount().GetValue())
			}
			fmt.Fprintf(w, "\t%s\t%s\t%s\t%s\t%s\n", amounts...)
		}),
	}
}
