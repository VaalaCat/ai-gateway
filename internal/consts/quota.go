package consts

// SettingKeyMinQuotaReserve 付费渠道放行所需的最低剩余额度(单位同 User.Quota)。
// Quota <= 该值即拦付费、只放免费。默认 0(扣到 0 才拦)。
const SettingKeyMinQuotaReserve = "min_quota_reserve"
