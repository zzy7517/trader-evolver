// Package types holds the shared structs for the pipeline + evolution logic,
// ported faithfully from tradex/pipeline/types.ts and tradex/evolution/types.ts.
package types

// ============================================================================
// Regime
// ============================================================================

type MarketRegime string
type VolatilityRegime string
type TrendRegime string

const (
	RiskOn  MarketRegime = "RISK_ON"
	RiskOff MarketRegime = "RISK_OFF"
	Neutral MarketRegime = "NEUTRAL"

	VolLow     VolatilityRegime = "LOW"
	VolMedium  VolatilityRegime = "MEDIUM"
	VolHigh    VolatilityRegime = "HIGH"
	VolExtreme VolatilityRegime = "EXTREME"

	TrendStrongUp   TrendRegime = "STRONG_UP"
	TrendUp         TrendRegime = "UP"
	TrendRange      TrendRegime = "RANGE"
	TrendDown       TrendRegime = "DOWN"
	TrendStrongDown TrendRegime = "STRONG_DOWN"
)

// RegimeIndicators mirrors the TS interface; nil pointers represent `null`.
type RegimeIndicators struct {
	VIX            *float64 `json:"vix"`
	ADX            *float64 `json:"adx"`
	FearGreed      *float64 `json:"fearGreed"`
	FundingRate    *float64 `json:"fundingRate"`
	LongShortRatio *float64 `json:"longShortRatio"`
	OIDelta1h      *float64 `json:"oiDelta1h"`
	DXY            *float64 `json:"dxy"`
}

type RegimeSignal struct {
	Market     MarketRegime     `json:"market"`
	Volatility VolatilityRegime `json:"volatility"`
	Trend      TrendRegime      `json:"trend"`
	Indicators RegimeIndicators `json:"indicators"`
	DetectedAt string           `json:"detectedAt"`
}

// ============================================================================
// Module Output
// ============================================================================

type SignalDirection string

const (
	SignalLong    SignalDirection = "LONG"
	SignalShort   SignalDirection = "SHORT"
	SignalNeutral SignalDirection = "NEUTRAL"
)

type KeyLevels struct {
	Support    []float64 `json:"support"`
	Resistance []float64 `json:"resistance"`
}

type ModuleOutput struct {
	ModuleID   string          `json:"moduleId"`
	Signal     SignalDirection `json:"signal"`
	Conviction float64         `json:"conviction"` // 0-100
	Entry      *float64        `json:"entry"`
	StopLoss   *float64        `json:"stopLoss"`
	TakeProfit *float64        `json:"takeProfit"`
	KeyLevels  KeyLevels       `json:"keyLevels"`
	Reasoning  string          `json:"reasoning"`
}

type ModuleRunResult struct {
	ModuleID     string       `json:"moduleId"`
	DarwinWeight float64      `json:"darwinWeight"`
	Output       ModuleOutput `json:"output"`
	TokensUsed   int          `json:"tokensUsed"`
	DurationMs   int64        `json:"durationMs"`
	Error        *string      `json:"error"`
}

// ============================================================================
// Pipeline Run
// ============================================================================

type PipelineTrigger string
type PipelineStatus string
type TradeAction string

const (
	TriggerCron   PipelineTrigger = "cron"
	TriggerManual PipelineTrigger = "manual"
	TriggerSignal PipelineTrigger = "signal"

	StatusRunning   PipelineStatus = "running"
	StatusCompleted PipelineStatus = "completed"
	StatusFailed    PipelineStatus = "failed"

	ActionOpenLong  TradeAction = "OPEN_LONG"
	ActionOpenShort TradeAction = "OPEN_SHORT"
	ActionClose     TradeAction = "CLOSE"
	ActionHold      TradeAction = "HOLD"
	ActionPass      TradeAction = "PASS"
)

type TradeDecision struct {
	Action           TradeAction `json:"action"`
	InstrumentKey    string      `json:"instrumentKey"`
	Entry            *float64    `json:"entry"`
	StopLoss         *float64    `json:"stopLoss"`
	TakeProfit       *float64    `json:"takeProfit"`
	PositionSizePct  *float64    `json:"positionSizePct"`
	RiskRewardRatio  *float64    `json:"riskRewardRatio"`
	Confidence       float64     `json:"confidence"` // 0-100
	ModulesAgreeing  int         `json:"modulesAgreeing"`
	ModulesTotal     int         `json:"modulesTotal"`
	SurvivedCRO      bool        `json:"survivedCRO"`
	CROObjections    []string    `json:"croObjections"`
	ReflexivityFlags []string    `json:"reflexivityFlags"`
	Reasoning        string      `json:"reasoning"`
}

type PipelineRun struct {
	ID            string            `json:"id"`
	TriggeredBy   PipelineTrigger   `json:"triggeredBy"`
	InstrumentKey string            `json:"instrumentKey"`
	Regime        RegimeSignal      `json:"regime"`
	StartedAt     string            `json:"startedAt"`
	CompletedAt   *string           `json:"completedAt"`
	Status        PipelineStatus    `json:"status"`
	ModuleResults []ModuleRunResult `json:"moduleResults"`
	Decision      *TradeDecision    `json:"decision"`
	TotalTokens   int               `json:"totalTokens"`
	TotalCostUsd  float64           `json:"totalCostUsd"`
	DurationMs    int64             `json:"durationMs"`
}

// ============================================================================
// Synthesis
// ============================================================================

type SynthesisInput struct {
	Regime        RegimeSignal
	ModuleResults []ModuleRunResult
	InstrumentKey string
	CurrentPrice  float64
}

type SynthesisOutput struct {
	AggregatedSignal   SignalDirection `json:"aggregatedSignal"`
	WeightedConviction float64         `json:"weightedConviction"`
	ModulesAgreeing    int             `json:"modulesAgreeing"`
	ModulesTotal       int             `json:"modulesTotal"`
	ConsensusEntry     *float64        `json:"consensusEntry"`
	ConsensusSL        *float64        `json:"consensusSL"`
	ConsensusTP        *float64        `json:"consensusTP"`
	Reasoning          string          `json:"reasoning"`
}

// ============================================================================
// CRO (Adversarial Review)
// ============================================================================

type CROInput struct {
	Synthesis      SynthesisOutput
	Regime         RegimeSignal
	InstrumentKey  string
	CurrentPrice   float64
	FundingRate    *float64
	LongShortRatio *float64
	OIDelta        *float64
}

type CROOutput struct {
	Approved           bool     `json:"approved"`
	Objections         []string `json:"objections"`
	ReflexivityFlags   []string `json:"reflexivityFlags"`
	RiskLevel          string   `json:"riskLevel"` // LOW | MEDIUM | HIGH | EXTREME
	AdjustedConviction float64  `json:"adjustedConviction"`
	Reasoning          string   `json:"reasoning"`
}

// ============================================================================
// Evolution (ported from evolution/types.ts)
// ============================================================================

type ModuleScore struct {
	ModuleID               string  `json:"moduleId"`
	DarwinWeight           float64 `json:"darwinWeight"` // 0.3 - 2.5
	Sharpe30d              float64 `json:"sharpe30d"`
	HitRate30d             float64 `json:"hitRate30d"` // 0-1
	TotalRecommendations   int     `json:"totalRecommendations"`
	ModificationsAttempted int     `json:"modificationsAttempted"`
	ModificationsKept      int     `json:"modificationsKept"`
	LastModifiedAt         *string `json:"lastModifiedAt"`
	UpdatedAt              string  `json:"updatedAt"`
}

type Recommendation struct {
	ID                    int64           `json:"id,omitempty"`
	ModuleID              string          `json:"moduleId"`
	InstrumentKey         string          `json:"instrumentKey"`
	Signal                SignalDirection `json:"signal"`
	Conviction            float64         `json:"conviction"`
	PriceAtRecommendation float64         `json:"priceAtRecommendation"`
	RecommendedAt         string          `json:"recommendedAt"`
	// Forward returns — filled in later by tracking.
	Return1d  *float64 `json:"return1d"`
	Return5d  *float64 `json:"return5d"`
	Return20d *float64 `json:"return20d"`
}

type DarwinWeightEntry struct {
	ModuleID   string   `json:"moduleId"`
	Weight     float64  `json:"weight"`
	Sharpe30d  *float64 `json:"sharpe30d"`
	HitRate30d *float64 `json:"hitRate30d"`
	UpdatedAt  string   `json:"updatedAt"`
}

type PromptModification struct {
	ID           int64    `json:"id,omitempty"`
	ModuleID     string   `json:"moduleId"`
	GitBranch    string   `json:"gitBranch"`
	Description  string   `json:"description"`
	BeforeSharpe float64  `json:"beforeSharpe"`
	AfterSharpe  *float64 `json:"afterSharpe"`
	Status       string   `json:"status"` // testing | kept | reverted
	CreatedAt    string   `json:"createdAt"`
	EvaluatedAt  *string  `json:"evaluatedAt"`
}

// DefaultModuleIDs lists the analysis modules in canonical order.
var DefaultModuleIDs = []string{
	"ict_trader",
	"chanlun_analyst",
	"wave_analyst",
	"indicator_analyst",
	"fundamental_analyst",
}

const (
	DefaultDarwinWeight = 1.0
	MinDarwinWeight     = 0.3
	MaxDarwinWeight     = 2.5
	WeightGrowthFactor  = 1.05
	WeightDecayFactor   = 0.95
)

// ============================================================================
// Historical data (collectors + store)
// ============================================================================

// Candle is one OHLCV bar, keyed by instrument + interval + open time.
// Mirrors tradex/domain/price_action.ts Candle (openTimeMs in epoch millis).
type Candle struct {
	InstrumentKey string  `json:"instrumentKey"`
	Interval      string  `json:"interval"` // e.g. "1d", "4h", "1h", "1m"
	OpenTimeMs    int64   `json:"openTimeMs"`
	Open          float64 `json:"open"`
	High          float64 `json:"high"`
	Low           float64 `json:"low"`
	Close         float64 `json:"close"`
	Volume        float64 `json:"volume"`
}

// DailyMacro holds a daily macro snapshot for a single series (one key per row).
// Used for VIX / DXY / S&P etc. fetched from Yahoo. Value is the daily close.
type DailyMacro struct {
	Series  string  `json:"series"` // e.g. "VIX", "DXY", "SPX"
	DateMs  int64   `json:"dateMs"` // midnight-UTC epoch millis for the day
	Close   float64 `json:"close"`
}

// FearGreed is one daily Crypto Fear & Greed Index reading from alternative.me.
type FearGreed struct {
	DateMs         int64  `json:"dateMs"` // day epoch millis
	Value          int    `json:"value"`  // 0-100
	Classification string `json:"classification"`
}

// Coverage summarizes the stored range for a (instrumentKey, interval) series.
type Coverage struct {
	InstrumentKey string `json:"instrumentKey"`
	Interval      string `json:"interval"`
	Count         int64  `json:"count"`
	FirstOpenMs   int64  `json:"firstOpenMs"`
	LastOpenMs    int64  `json:"lastOpenMs"`
}
