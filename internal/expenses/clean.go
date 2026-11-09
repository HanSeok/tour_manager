// Package expenses validates, stores and edits trip expenses (one row per entry in the 지출 tab).
// Validation is a port of cleanExpense_ in apps-script/Expenses.js.
package expenses

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tourmanager/internal/tripconfig"
)

const (
	preTripDays = 365 // pre-paid flights/hotels may be booked up to a year ahead
	maxAmount   = 100000000
	maxItemLen  = 60
)

// Categories are fixed (the front end offers exactly these).
var Categories = []string{"식비", "교통", "숙박", "쇼핑", "관광", "기타"}

var (
	numberRe   = regexp.MustCompile(`^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$`)
	clientIDRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// Input is what the dashboard submits. Amount arrives as a JSON number or a numeric string.
type Input struct {
	ID       string `json:"id"`
	Date     string `json:"date"`
	Category string `json:"category"`
	Item     string `json:"item"`
	Amount   any    `json:"amount"`
	Payer    string `json:"payer"`
	Method   string `json:"method"`
	Currency string `json:"currency"` // "home" = typed in the home currency; anything else = local
}

// Cleaned is a validated entry, amounts in both currencies.
type Cleaned struct {
	Date, Category, Item, Payer, Method, Currency string
	Amount, Home                                  float64
}

// ValidationError is a user-facing input problem (HTTP 422).
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

// number mimics JS Number(v) for the shapes JSON can carry; ok=false means NaN.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, true // Number("") === 0 (rejected later as not > 0)
		}
		if !numberRe.MatchString(s) {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil && !math.IsInf(f, 0)
	}
	return 0, false
}

func roundTo(v float64, d int) float64 {
	p := math.Pow10(d)
	return math.Floor(v*p+0.5) / p // Math.round
}

func hasDecimals(v float64, d int) bool {
	p := math.Pow10(d)
	return math.Abs(math.Round(v*p)-v*p) < 1e-6
}

func addDays(iso string, n int) string {
	t, _ := time.Parse("2006-01-02", iso)
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Clean validates one submitted entry against the trip settings.
func Clean(cfg tripconfig.Config, in Input) (Cleaned, error) {
	isHome := in.Currency == "home"
	decimals := cfg.CurrencyDecimals
	if isHome {
		decimals = cfg.HomeDecimals
	}
	if !tripconfig.IsRealDate(in.Date) || in.Date < addDays(cfg.Start, -preTripDays) || in.Date > cfg.End {
		return Cleaned{}, invalid("날짜는 여행 1년 전부터 종료일까지만 됩니다")
	}
	value, ok := number(in.Amount)
	if !ok || value <= 0 || value > maxAmount || !hasDecimals(value, decimals) {
		if decimals > 0 {
			return Cleaned{}, invalid("금액은 0보다 큰 숫자(소수 %d자리까지)여야 합니다", decimals)
		}
		return Cleaned{}, invalid("금액은 0보다 큰 숫자(정수)여야 합니다")
	}
	if !contains(Categories, in.Category) || !contains(cfg.Payers, in.Payer) || !contains(cfg.Methods, in.Method) {
		return Cleaned{}, invalid("분류/결제자/결제수단 값이 올바르지 않습니다")
	}
	amount, home := value, roundTo(value*cfg.Rate, cfg.HomeDecimals)
	if isHome {
		amount, home = roundTo(value/cfg.Rate, cfg.CurrencyDecimals), value
	} else {
		amount = roundTo(value, cfg.CurrencyDecimals)
	}
	item := strings.TrimSpace(in.Item)
	if r := []rune(item); len(r) > maxItemLen {
		item = string(r[:maxItemLen])
	}
	if item == "" {
		item = in.Category
	}
	currency := cfg.LocalCurrency
	if isHome {
		currency = cfg.HomeSymbol
	}
	return Cleaned{Date: in.Date, Category: in.Category, Item: item, Payer: in.Payer, Method: in.Method, Currency: currency, Amount: amount, Home: home}, nil
}

// NewClientID is true for a UUID the dashboard generated (used to make retries idempotent).
func IsClientID(s string) bool { return clientIDRe.MatchString(s) }
