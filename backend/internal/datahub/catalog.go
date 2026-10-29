package datahub

func definitions() []Definition {
	items := []Definition{
		{ID: "sina", Name: "新浪公开期货快照", Kind: "market", Description: "实际玉米、淀粉、生猪、豆粕合约快照；公开网页接口，非交易所授权实时行情服务。", OfficialURL: "https://finance.sina.com.cn/futures/", CostNote: "当前公开接口无需账号；没有实时性、持续可用或完整性承诺。", LicenseNote: "仅作个人研究参考。代码开源不授予数据商用、转发或交易使用权；生产交易请换用授权源。", MinIntervalSeconds: 0.5, MaxIntervalSeconds: 3600, DefaultInterval: 0.5},
		{ID: "liaoning", Name: "辽宁粮储 · 玉米收购监测", Kind: "fundamental", Description: "官方周度价格报表；全省14市平均价，包含玉米收购/进厂等口径，不是某厂当天门口价。", OfficialURL: "https://lcj.ln.gov.cn/lswzcb/xwdt24/tjxx/index.shtml", CostNote: "公开发布，无需账号；低频获取最新报表。", LicenseNote: "保留发布机构、原始链接、监测日期与统计口径；公开监测价不代表可成交报价。", MinIntervalSeconds: 21600, MaxIntervalSeconds: 604800, DefaultInterval: 43200},
		{ID: "moa", Name: "农业农村部 · 饲料与畜产品价格", Kind: "fundamental", Description: "全国500个县集贸市场和采集点周报，涵盖玉米、豆粕、生猪等关联现货价格。", OfficialURL: "https://xmsyj.moa.gov.cn/jcyj/", CostNote: "公开发布，无需账号；发布与统计日期分开显示。", LicenseNote: "全国监测均价与具体交割品/企业现货不同，不能直接解释为可套利价格。", MinIntervalSeconds: 21600, MaxIntervalSeconds: 604800, DefaultInterval: 43200},
		{ID: "boyar_prices", Name: "博亚和讯 · 企业公开报价", Kind: "fundamental", Description: "公开企业价格表及可解析收购通知；保留原页单位与水分、票据条件，未来生效通知只作事件。", OfficialURL: "https://www.boyar.cn/column/7.html", CostNote: "当前所接公开栏目无需账号；不采会员内容，非连续实时接口。", LicenseNote: "公开可访问不等于商业再分发授权。部分汇总表未标价格单位，展示原值并排除基差计算。", MinIntervalSeconds: 21600, MaxIntervalSeconds: 604800, DefaultInterval: 43200},
		{ID: "boyar_arrivals", Name: "博亚和讯 · 山东晨间到车", Kind: "fundamental", Description: "最新公开山东晨间深加工企业到车记录；原页未细分累计到厂/待卸时保留该不确定性。", OfficialURL: "https://www.boyar.cn/column/7.html", CostNote: "公开延迟记录，无需账号；采集不增加上游发布频率。", LicenseNote: "样本及统计窗口以原文为准，车辆数不等于卸粮吨数，也不代表全国供给。", MinIntervalSeconds: 21600, MaxIntervalSeconds: 604800, DefaultInterval: 43200},
		{ID: "import_auction", Name: "中储粮 · 进口玉米拍卖结果", Kind: "fundamental", Description: "每日检查最近公开的进口玉米竞价销售结果，只保存原文披露的成交指标；没有新场次不补零。", OfficialURL: "https://www.esinograin.com", CostNote: "当前公开交易结果无需账号；这里只读查询，不参与竞价或登录交易。", LicenseNote: "进口存量在境内的竞价销售不是当日新签境外合同或通关进口量；未披露吨数/成交价时不推算。", MinIntervalSeconds: 43200, MaxIntervalSeconds: 604800, DefaultInterval: 86400},
		{ID: "weather", Name: "Open-Meteo · 产区天气", Kind: "weather", Description: "东北与华北5个点位，未来3日降水及最高/最低温；保留采集批次。", OfficialURL: "https://open-meteo.com/en/terms", CostNote: "免费接口限非商业用途；商业产品或机构商业研究需购买服务或自行部署。", LicenseNote: "Weather data by Open-Meteo.com，CC BY 4.0；点位不代表全省，预测目标日不是数据更新时间。", MinIntervalSeconds: 3600, MaxIntervalSeconds: 86400, DefaultInterval: 3600},
		{ID: "market_bridge", Name: "授权行情 · TqSdk / JSON桥接", Kind: "market", Description: "运行随项目提供的只读TqSdk桥接，或接入符合项目JSON契约的授权行情服务。", OfficialURL: "https://doc.shinnytech.com/tqsdk/latest/quickstart.html", AccountRequired: true, CostNote: "TqSdk需要快期账户；免费/专业版的实时、历史、并发及云端权益以账户和当前官方说明为准，专业权益可能付费。", LicenseNote: "本入口不自动购买或申请权限；CTP需期货公司行情接入授权，行情账户与交易资金账户应区分。", ConfigurableEndpoint: true, MinIntervalSeconds: 0.5, MaxIntervalSeconds: 3600, DefaultInterval: 0.5},
		{ID: "industry_bridge", Name: "企业收购价 / 到车量 · 授权桥接", Kind: "fundamental", Description: "接入供应商或企业授权的JSON出口，记录厂名、区域、到车统计窗口及报价水分/等级口径。", OfficialURL: "https://www.mysteel.com/fwbj.html", AccountRequired: true, CostNote: "Mysteel/卓创等通常需订阅或单独数据合作；具体API字段、价格及采集/存储权限需向供应商询价。", LicenseNote: "网页会员不等于API/批量采集权限。本入口接项目标准JSON，未声称已对接任何未取得的供应商私有API。", ConfigurableEndpoint: true, MinIntervalSeconds: 60, MaxIntervalSeconds: 86400, DefaultInterval: 300},
	}
	for i := range items {
		items[i].RequestPolicy = requestPolicy(items[i].ID)
	}
	return items
}

func requestPolicy(id string) string {
	common := " 自动采集与报告共用已保存的间隔和失败退避；低频源手动测试/同步共用5秒成功批次，行情源按配置间隔复用（最多5秒）；失败后本地冷却至少5秒。采集间隔不代表来源延迟或授权额度。"
	policies := map[string]string{
		"sina":            "未找到新浪公开期货快照接口的官方数值额度或SLA。默认/最低0.5秒（目标120次/分钟）是本项目自动采集设置，并非来源授权额度；响应慢或失败退避时实际次数会降低；一轮合并实际合约为一次请求。",
		"liaoning":        "官方周度报表，默认12小时检查一次、最低6小时；此为本地检查策略，不是来源公布的调用额度。",
		"moa":             "官方周度报表，默认12小时检查一次、最低6小时；此为本地检查策略，不是来源公布的调用额度。",
		"boyar_prices":    "未找到博亚公开网页的官方数值额度。默认12小时、最低6小时；每轮栏目加最多3篇公开报告，文章间隔至少0.5秒。",
		"boyar_arrivals":  "未找到博亚公开网页的官方数值额度。默认12小时、最低6小时；每轮栏目加最多3篇公开报告，文章间隔至少0.5秒；与报价来源合计请求。",
		"import_auction":  "未找到中储粮公开结果查询的数值额度。默认24小时、最低12小时，一轮栏目与最新详情最多2次请求；采集器另有60秒缓存，繁忙响应按失败退避。",
		"weather":         "Open-Meteo免费服务公开限额：每分钟600、每小时5000、每日10000、每月300000次；仅限非商业用途。默认/最低每小时一轮5点，通常5次HTTP请求；仍须按官方加权计数与当前条款核验。",
		"market_bridge":   "默认/最低0.5秒（目标120次/分钟）轮询自有授权桥接；桥接HTTP间隔不代表TqSdk行情推送间隔，账号与供应商配额需另行遵守。",
		"industry_bridge": "默认300秒、最低60秒轮询授权桥接；实际调用额度、统计发布频率以供应商协议为准。",
	}
	return policies[id] + common
}
