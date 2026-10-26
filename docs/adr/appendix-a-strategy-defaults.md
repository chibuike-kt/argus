# Consolidated strategy defaults

- Source: ARGUS Master ADR, section 14
- Version: 0.1.0-draft
- Last updated: 26 October 2026

---

The following table records the currently selected analytical conventions. A selected convention does not imply that every parameter or edge case is finalized.

| Component | Selected direction | Unresolved details |
| --- | --- | --- |
| Base candle interval | 1 minute | Provider coverage and boundary handling |
| Analysis intervals | 1m, 5m, 15m, 1h, 4h, 1d | Explicit strategy timeframe mapping |
| Swing detection | Symmetric fractal, default 2-left/2-right | Tie handling and confirmation edge cases |
| Trend classification | Confirmed swing sequences and structure | Exact trend states and transitions |
| BOS/CHOCH | Close beyond confirmed swing | Precise BOS versus CHOCH semantics |
| Supply/demand | Base followed by qualifying impulse | Base length and impulse thresholds |
| Zone boundaries | Full base wick range | Edge-case selection rules |
| Support/resistance | Swing clusters, ATR-relative tolerance | Cluster merging and thresholds |
| Minimum S/R evidence | Two distinct confirmed swings | Exact clustering policy |
| ATR | Default period 14, configurable | Formula, smoothing and warm-up |
| Displacement | Combined ATR threshold and structural evidence | Numerical thresholds |
| Equal highs/lows | Clustered levels | Tolerance and merge behavior |
| Liquidity sweep | Breach followed by close back within a window | Window and boundary rules |
| Order block | Last opposite candle before qualifying break | Eligibility and selection details |
| FVG | Three-candle wick-to-wick non-overlap | Minimum gap and edge cases |
| POI retests | Track as evidence | Exact event counting |
| POI invalidation | Closed-candle break of boundary | Boundary-specific implementation |
| Required conditions | All mandatory conditions pass | Group semantics |
| Unevaluable condition | Block confirmation | Exact recovery and reevaluation policy |
| Initial POIs | Supply/demand and support/resistance | Whether future strategy versions add POI types |
| Confluence | Strategy-specific | Initial mandatory confluence list |
| Conflicting evidence | Expose conflicts | Exact presentation and transition rules |
| Strategy profile | One configurable versioned profile | Initial approved parameters |
| Entry | Strategy-defined | Exact entry models |
| Limit validity | Linked to POI expiry | Expiry precedence |
| Stop-loss | Invalidation level plus volatility buffer | Multiplier and rounding |
| Targets | Multiple structural targets | Selection and overlap rules |
| Position sizing | Instrument-aware | Verified contract metadata and conversions |
| Risk-to-reward | Display-only | Presentation |
| Costs | Estimate only when assumptions are known | Provider and model selection |
| Confirmation | Closed candles with valid data | Full condition configuration |
| Re-entry | Allowed after terminal state when rules permit | Exact eligible states and cooldown |
| Plan changes | New linked version for material changes | Materiality policy |
| Corrected candles | Append revision and reevaluate affected dependencies | Exact propagation implementation |
