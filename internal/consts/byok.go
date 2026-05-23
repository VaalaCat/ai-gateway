package consts

// SystemBYOKBaseURLs 是系统内置的 BYOK BaseURL 前缀集合，随版本演进。
// 新增 provider 直接在这里 append；老部署升级即自动生效。
// 不写入 settings 表；admin 不可禁用某条（如未来需要禁用能力，加
// byok_system_baseurl_disabled setting 走黑名单并集）。
var SystemBYOKBaseURLs = []string{
	"https://api.openai.com",
	"https://api.anthropic.com",
}

// BYOK 相关 settings 表 key。Setting.Value 是 string，按 typed runtime 语义
// 由 LookupBool/Int/String/Float 解析。所有读写此 5 key 的代码必须用这些常量，
// 避免拼写漂移；这些 key 同时在 settings.go settingDefs 白名单 + seed.go 默认
// 写入 + 各 LookupXxx 调用点共享。
const (
	SettingKeyBYOKEnabled            = "byok_enabled"
	SettingKeyBYOKMaxChannelsPerUser = "byok_max_channels_per_user"
	SettingKeyBYOKBillingMode        = "byok_billing_mode"
	SettingKeyBYOKServiceFeeRatio    = "byok_service_fee_ratio"
	SettingKeyBYOKBaseURLAllowlist   = "byok_base_url_allowlist"
)

// SettingKeyBYOKBillingMode 的合法枚举值。settings.go 的 validate 与
// settler.go 的分支判断都引用同一常量，禁止任一边裸字面值。
const (
	BYOKBillingModeFree       = "free"
	BYOKBillingModeServiceFee = "service_fee"
)

// BYOK 各 setting 的默认值。存在两种形式：
//   - *Str：DB 持久化形式（Setting.Value 是 string column）。被 settings.go
//     settingDefs 的 Default 字段与 seed.go SeedBYOKSettings 共用，确保 admin
//     未改过任何值时落库和兜底完全一致。
//   - typed (Bool/Int/Float)：runtime fallback 形式。被 validate.go / settler.go
//     的 LookupBool/LookupInt/LookupFloat 第二参数共用——LookupXxx 在 setting
//     不存在或解析失败时返回这个值，因此必须与 *Str 解析后语义相等。
//
// 双形式重复存在以避免每次调用 LookupXxx 都跑 strconv.Atoi/ParseFloat；改默认
// 值时务必同步两组常量。
const (
	BYOKDefaultEnabledStr            = "true"
	BYOKDefaultEnabledBool           = true
	BYOKDefaultMaxChannelsPerUserStr = "20"
	BYOKDefaultMaxChannelsPerUserInt = 20
	BYOKDefaultBillingMode           = BYOKBillingModeFree
	BYOKDefaultServiceFeeRatioStr    = "0.1"
	BYOKDefaultServiceFeeRatioFloat  = 0.1
	BYOKDefaultBaseURLAllowlistStr   = "[]"
)
