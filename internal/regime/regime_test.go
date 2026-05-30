package regime

import (
	"testing"

	"trader-evolver/internal/types"
)

func f(v float64) *float64 { return &v }

func TestDetectMarketRegime(t *testing.T) {
	cases := []struct {
		name string
		ind  types.RegimeIndicators
		want types.MarketRegime
	}{
		{"low vix risk on", types.RegimeIndicators{VIX: f(14)}, types.RiskOn},                  // +2
		{"high vix risk off", types.RegimeIndicators{VIX: f(31)}, types.RiskOff},               // -2
		{"empty neutral", types.RegimeIndicators{}, types.Neutral},                             // 0
		{"mid vix neutral", types.RegimeIndicators{VIX: f(22)}, types.Neutral},                 // 0
		{"vix18 + greed", types.RegimeIndicators{VIX: f(18), FearGreed: f(72)}, types.Neutral}, // 1+1=2 -> RiskOn
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectMarketRegime(c.ind)
			// Special: vix18(+1)+greed72(+1)=2 => RiskOn
			if c.name == "vix18 + greed" {
				c.want = types.RiskOn
			}
			if got != c.want {
				t.Errorf("got %s want %s", got, c.want)
			}
		})
	}
}

func TestDetectVolatility(t *testing.T) {
	cases := []struct {
		vix  *float64
		want types.VolatilityRegime
	}{
		{nil, types.VolMedium},
		{f(40), types.VolExtreme},
		{f(30), types.VolHigh},
		{f(20), types.VolMedium},
		{f(10), types.VolLow},
	}
	for _, c := range cases {
		got := detectVolatility(types.RegimeIndicators{VIX: c.vix})
		if got != c.want {
			t.Errorf("vix=%v got %s want %s", c.vix, got, c.want)
		}
	}
}

func TestDetectTrend(t *testing.T) {
	cases := []struct {
		name string
		ind  types.RegimeIndicators
		want types.TrendRegime
	}{
		{"no adx -> range", types.RegimeIndicators{}, types.TrendRange},
		{"strong up", types.RegimeIndicators{ADX: f(45), FearGreed: f(60)}, types.TrendStrongUp},
		{"strong down", types.RegimeIndicators{ADX: f(45), FearGreed: f(40)}, types.TrendStrongDown},
		{"up", types.RegimeIndicators{ADX: f(30), FearGreed: f(55)}, types.TrendUp},
		{"down", types.RegimeIndicators{ADX: f(30), FearGreed: f(45)}, types.TrendDown},
		{"range", types.RegimeIndicators{ADX: f(20)}, types.TrendRange},
		{"adx no fg defaults neutral->down side", types.RegimeIndicators{ADX: f(30)}, types.TrendDown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectTrend(c.ind)
			if got != c.want {
				t.Errorf("got %s want %s", got, c.want)
			}
		})
	}
}
