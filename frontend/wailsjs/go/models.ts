export namespace auth {
	
	export class AuthState {
	    loggedIn: boolean;
	    username: string;
	    nickname: string;
	    role: string;
	    guest: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AuthState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.loggedIn = source["loggedIn"];
	        this.username = source["username"];
	        this.nickname = source["nickname"];
	        this.role = source["role"];
	        this.guest = source["guest"];
	    }
	}
	export class Session {
	    userId: number;
	    username: string;
	    nickname: string;
	    role: string;
	    loginAt: time.Time;
	    guest: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Session(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.userId = source["userId"];
	        this.username = source["username"];
	        this.nickname = source["nickname"];
	        this.role = source["role"];
	        this.loginAt = this.convertValues(source["loginAt"], time.Time);
	        this.guest = source["guest"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LoginResult {
	    ok: boolean;
	    message: string;
	    session?: Session;
	
	    static createFrom(source: any = {}) {
	        return new LoginResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.message = source["message"];
	        this.session = this.convertValues(source["session"], Session);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace checkout {
	
	export class Spec {
	    name: string;
	    pricePerKg: number;
	    pricePerItem: number;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new Spec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.pricePerKg = source["pricePerKg"];
	        this.pricePerItem = source["pricePerItem"];
	        this.total = source["total"];
	    }
	}
	export class ProSPecs {
	    SID: number;
	    Name: string;
	    Specs: Spec[];
	
	    static createFrom(source: any = {}) {
	        return new ProSPecs(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.SID = source["SID"];
	        this.Name = source["Name"];
	        this.Specs = this.convertValues(source["Specs"], Spec);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CheckoutResponse {
	    FieldList: string[];
	    PBMap: Record<string, Array<string>>;
	    Count: number;
	    ProSPecs: ProSPecs[];
	    TableData: string[];
	    TotalRow: number[];
	    PricePerKgRow: number[];
	    PricePerItemRow: number[];
	    TotalAmountRow: number[];
	
	    static createFrom(source: any = {}) {
	        return new CheckoutResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.FieldList = source["FieldList"];
	        this.PBMap = source["PBMap"];
	        this.Count = source["Count"];
	        this.ProSPecs = this.convertValues(source["ProSPecs"], ProSPecs);
	        this.TableData = source["TableData"];
	        this.TotalRow = source["TotalRow"];
	        this.PricePerKgRow = source["PricePerKgRow"];
	        this.PricePerItemRow = source["PricePerItemRow"];
	        this.TotalAmountRow = source["TotalAmountRow"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProPriceRequest {
	    productName: string;
	    spec: string;
	    total: number;
	    pricePerKg: number;
	    pricePerItem: number;
	    totalAmount: number;
	
	    static createFrom(source: any = {}) {
	        return new ProPriceRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.productName = source["productName"];
	        this.spec = source["spec"];
	        this.total = source["total"];
	        this.pricePerKg = source["pricePerKg"];
	        this.pricePerItem = source["pricePerItem"];
	        this.totalAmount = source["totalAmount"];
	    }
	}
	

}

export namespace core {
	
	export class CfgWatchStatus {
	    enabled: boolean;
	    intervalSeconds: number;
	    checks: number;
	    changes: number;
	    lastCheckAt: string;
	    lastChangedAt: string;
	    lastChange: string;
	    nextCheckAt: string;
	    lastError: string;
	    revision: string;
	    cfgMd5: string;
	    fastSkips: number;
	    fullLoads: number;
	    partialLoads: number;
	    lastSections: string;
	    lastMode: string;
	
	    static createFrom(source: any = {}) {
	        return new CfgWatchStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.intervalSeconds = source["intervalSeconds"];
	        this.checks = source["checks"];
	        this.changes = source["changes"];
	        this.lastCheckAt = source["lastCheckAt"];
	        this.lastChangedAt = source["lastChangedAt"];
	        this.lastChange = source["lastChange"];
	        this.nextCheckAt = source["nextCheckAt"];
	        this.lastError = source["lastError"];
	        this.revision = source["revision"];
	        this.cfgMd5 = source["cfgMd5"];
	        this.fastSkips = source["fastSkips"];
	        this.fullLoads = source["fullLoads"];
	        this.partialLoads = source["partialLoads"];
	        this.lastSections = source["lastSections"];
	        this.lastMode = source["lastMode"];
	    }
	}
	export class CfgRefreshResult {
	    status: CfgWatchStatus;
	    changed: boolean;
	    summary: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new CfgRefreshResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = this.convertValues(source["status"], CfgWatchStatus);
	        this.changed = source["changed"];
	        this.summary = source["summary"];
	        this.error = source["error"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace dashboard {
	
	export class CustomerValue {
	    name: string;
	    orders: number;
	    weight: number;
	
	    static createFrom(source: any = {}) {
	        return new CustomerValue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.orders = source["orders"];
	        this.weight = source["weight"];
	    }
	}
	export class DashboardRequest {
	    period: string;
	    start: time.Time;
	    end: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new DashboardRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.period = source["period"];
	        this.start = this.convertValues(source["start"], time.Time);
	        this.end = this.convertValues(source["end"], time.Time);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProvinceCount {
	    name: string;
	    orders: number;
	
	    static createFrom(source: any = {}) {
	        return new ProvinceCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.orders = source["orders"];
	    }
	}
	export class NameValue {
	    name: string;
	    value: number;
	    orders: number;
	
	    static createFrom(source: any = {}) {
	        return new NameValue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.value = source["value"];
	        this.orders = source["orders"];
	    }
	}
	export class TrendPoint {
	    key: string;
	    sortKey: string;
	    orders: number;
	    weight: number;
	
	    static createFrom(source: any = {}) {
	        return new TrendPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.sortKey = source["sortKey"];
	        this.orders = source["orders"];
	        this.weight = source["weight"];
	    }
	}
	export class Summary {
	    totalOrders: number;
	    totalWeight: number;
	    repurchaseRate: number;
	    customerCount: number;
	    productCount: number;
	    unknownProvince: number;
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.totalOrders = source["totalOrders"];
	        this.totalWeight = source["totalWeight"];
	        this.repurchaseRate = source["repurchaseRate"];
	        this.customerCount = source["customerCount"];
	        this.productCount = source["productCount"];
	        this.unknownProvince = source["unknownProvince"];
	    }
	}
	export class DashboardResponse {
	    summary: Summary;
	    trend: TrendPoint[];
	    topProducts: NameValue[];
	    topCustomers: CustomerValue[];
	    province: ProvinceCount[];
	    generatedAt: string;
	    unmappedProducts: NameValue[];
	    belongReady: boolean;
	    loading: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DashboardResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.summary = this.convertValues(source["summary"], Summary);
	        this.trend = this.convertValues(source["trend"], TrendPoint);
	        this.topProducts = this.convertValues(source["topProducts"], NameValue);
	        this.topCustomers = this.convertValues(source["topCustomers"], CustomerValue);
	        this.province = this.convertValues(source["province"], ProvinceCount);
	        this.generatedAt = source["generatedAt"];
	        this.unmappedProducts = this.convertValues(source["unmappedProducts"], NameValue);
	        this.belongReady = source["belongReady"];
	        this.loading = source["loading"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	

}

export namespace main {
	
	export class OrderActionRuleDTO {
	    id: number;
	    keyword: string;
	    action: string;
	    field: string;
	    valueRule: string;
	    scope: string;
	    priority: number;
	    enabled: boolean;
	    sortOrder: number;
	    remark: string;
	
	    static createFrom(source: any = {}) {
	        return new OrderActionRuleDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.keyword = source["keyword"];
	        this.action = source["action"];
	        this.field = source["field"];
	        this.valueRule = source["valueRule"];
	        this.scope = source["scope"];
	        this.priority = source["priority"];
	        this.enabled = source["enabled"];
	        this.sortOrder = source["sortOrder"];
	        this.remark = source["remark"];
	    }
	}

}

export namespace models {
	
	export class ProductSpec {
	    name: string;
	    weight: number;
	
	    static createFrom(source: any = {}) {
	        return new ProductSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.weight = source["weight"];
	    }
	}
	export class AddressInfo {
	    id: number;
	    createdAt: time.Time;
	    updatedAt: time.Time;
	    // Go type: gorm
	    deletedAt: any;
	    VID: number;
	    "收件人": string;
	    "手机号": string;
	    "收货地址": string;
	    "商品规格": ProductSpec[];
	    "备注": string;
	    Isstorage: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AddressInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.createdAt = this.convertValues(source["createdAt"], time.Time);
	        this.updatedAt = this.convertValues(source["updatedAt"], time.Time);
	        this.deletedAt = this.convertValues(source["deletedAt"], null);
	        this.VID = source["VID"];
	        this["收件人"] = source["收件人"];
	        this["手机号"] = source["手机号"];
	        this["收货地址"] = source["收货地址"];
	        this["商品规格"] = this.convertValues(source["商品规格"], ProductSpec);
	        this["备注"] = source["备注"];
	        this.Isstorage = source["Isstorage"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace order {
	
	export class OrderQueryRequest {
	    limit: number;
	    page: number;
	    key: string;
	    order: string;
	    Addresssinfo: models.AddressInfo;
	    startTime: time.Time;
	    endTime: time.Time;
	
	    static createFrom(source: any = {}) {
	        return new OrderQueryRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.limit = source["limit"];
	        this.page = source["page"];
	        this.key = source["key"];
	        this.order = source["order"];
	        this.Addresssinfo = this.convertValues(source["Addresssinfo"], models.AddressInfo);
	        this.startTime = this.convertValues(source["startTime"], time.Time);
	        this.endTime = this.convertValues(source["endTime"], time.Time);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class OrderQueryResponse {
	    list: ordertutil.ExceladdressInfo[];
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new OrderQueryResponse(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.list = this.convertValues(source["list"], ordertutil.ExceladdressInfo);
	        this.count = source["count"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace orderschema {
	
	export class ExtractRule {
	    kind: string;
	    labels?: string[];
	    regex?: string;
	    hint?: string;
	
	    static createFrom(source: any = {}) {
	        return new ExtractRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.labels = source["labels"];
	        this.regex = source["regex"];
	        this.hint = source["hint"];
	    }
	}
	export class FieldDef {
	    key: string;
	    label: string;
	    type: string;
	    required: boolean;
	    builtin: boolean;
	    indexed: boolean;
	    aliases?: string[];
	    extract: ExtractRule;
	    unit?: string;
	    options?: string[];
	    sort: number;
	    deprecated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FieldDef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.type = source["type"];
	        this.required = source["required"];
	        this.builtin = source["builtin"];
	        this.indexed = source["indexed"];
	        this.aliases = source["aliases"];
	        this.extract = this.convertValues(source["extract"], ExtractRule);
	        this.unit = source["unit"];
	        this.options = source["options"];
	        this.sort = source["sort"];
	        this.deprecated = source["deprecated"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ItemDef {
	    enabled: boolean;
	    name_labels?: string[];
	    quantity_unit?: string;
	    allow_attrs: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ItemDef(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.name_labels = source["name_labels"];
	        this.quantity_unit = source["quantity_unit"];
	        this.allow_attrs = source["allow_attrs"];
	    }
	}
	export class Signature {
	    keywords?: string[];
	    labels?: string[];
	    separators?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Signature(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.keywords = source["keywords"];
	        this.labels = source["labels"];
	        this.separators = source["separators"];
	    }
	}

}

export namespace ordertutil {
	
	export class ExceladdressInfo {
	    createdAt: time.Time;
	    VID: number;
	    "收件人": string;
	    "手机号": string;
	    "收货地址": string;
	    "商品规格": string;
	    "备注": string;
	
	    static createFrom(source: any = {}) {
	        return new ExceladdressInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.createdAt = this.convertValues(source["createdAt"], time.Time);
	        this.VID = source["VID"];
	        this["收件人"] = source["收件人"];
	        this["手机号"] = source["手机号"];
	        this["收货地址"] = source["收货地址"];
	        this["商品规格"] = source["商品规格"];
	        this["备注"] = source["备注"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace schemaservice {
	
	export class InferInput {
	    samples: string[];
	    id: string;
	    name: string;
	    useAi: boolean;
	    provider: string;
	
	    static createFrom(source: any = {}) {
	        return new InferInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.samples = source["samples"];
	        this.id = source["id"];
	        this.name = source["name"];
	        this.useAi = source["useAi"];
	        this.provider = source["provider"];
	    }
	}
	export class OrderQueryOptions {
	    templateId: string;
	    status: string;
	    from: string;
	    to: string;
	    limit: number;
	
	    static createFrom(source: any = {}) {
	        return new OrderQueryOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.templateId = source["templateId"];
	        this.status = source["status"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.limit = source["limit"];
	    }
	}
	export class RunOptions {
	    from: string;
	    to: string;
	    provider: string;
	    source: string;
	    dataDir: string;
	    dryRun: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RunOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.provider = source["provider"];
	        this.source = source["source"];
	        this.dataDir = source["dataDir"];
	        this.dryRun = source["dryRun"];
	    }
	}
	export class SaveTemplateInput {
	    id: string;
	    name: string;
	    description: string;
	    fields: orderschema.FieldDef[];
	    items: orderschema.ItemDef;
	    signatures: orderschema.Signature[];
	
	    static createFrom(source: any = {}) {
	        return new SaveTemplateInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.description = source["description"];
	        this.fields = this.convertValues(source["fields"], orderschema.FieldDef);
	        this.items = this.convertValues(source["items"], orderschema.ItemDef);
	        this.signatures = this.convertValues(source["signatures"], orderschema.Signature);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace time {
	
	export class Time {
	
	
	    static createFrom(source: any = {}) {
	        return new Time(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}

export namespace version {
	
	export class ReleaseInfo {
	    tag: string;
	    name: string;
	    body: string;
	    published_at: string;
	
	    static createFrom(source: any = {}) {
	        return new ReleaseInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tag = source["tag"];
	        this.name = source["name"];
	        this.body = source["body"];
	        this.published_at = source["published_at"];
	    }
	}

}

export namespace wechatorder {
	
	export class ActionOrderItem {
	    id: string;
	    action: string;
	    actionLabel: string;
	    sender: string;
	    messageTime: string;
	    rawContent: string;
	    quote: string;
	    reason: string;
	    confidence: string;
	    targetLabel: string;
	    matchedBy: string;
	    status: string;
	    changes: string;
	    reviewId: string;
	
	    static createFrom(source: any = {}) {
	        return new ActionOrderItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.action = source["action"];
	        this.actionLabel = source["actionLabel"];
	        this.sender = source["sender"];
	        this.messageTime = source["messageTime"];
	        this.rawContent = source["rawContent"];
	        this.quote = source["quote"];
	        this.reason = source["reason"];
	        this.confidence = source["confidence"];
	        this.targetLabel = source["targetLabel"];
	        this.matchedBy = source["matchedBy"];
	        this.status = source["status"];
	        this.changes = source["changes"];
	        this.reviewId = source["reviewId"];
	    }
	}
	export class AutoOrderDetail {
	    sender: string;
	    messageTime: string;
	    orderDate: string;
	    isToday: boolean;
	    score: number;
	    isOrder: boolean;
	    dateConfirmed: boolean;
	    needsReview: boolean;
	    confidence: string;
	    disposition: string;
	    reason: string;
	    orderCount: number;
	    recipient: string;
	    specFixed: boolean;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new AutoOrderDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sender = source["sender"];
	        this.messageTime = source["messageTime"];
	        this.orderDate = source["orderDate"];
	        this.isToday = source["isToday"];
	        this.score = source["score"];
	        this.isOrder = source["isOrder"];
	        this.dateConfirmed = source["dateConfirmed"];
	        this.needsReview = source["needsReview"];
	        this.confidence = source["confidence"];
	        this.disposition = source["disposition"];
	        this.reason = source["reason"];
	        this.orderCount = source["orderCount"];
	        this.recipient = source["recipient"];
	        this.specFixed = source["specFixed"];
	        this.error = source["error"];
	    }
	}
	export class ReviewOrderItem {
	    id: string;
	    sender: string;
	    messageTime: string;
	    messageDate: string;
	    orderDate: string;
	    reason: string;
	    confidence: string;
	    rawContent: string;
	    orderCount: number;
	    orders: ordertutil.ExceladdressInfo[];
	    kind?: string;
	    actionType?: string;
	    actionStatus?: string;
	    matchBy?: string;
	    changes?: string;
	    quote?: string;
	    before?: ordertutil.ExceladdressInfo[];
	    after?: ordertutil.ExceladdressInfo[];
	    candidates?: string[];
	
	    static createFrom(source: any = {}) {
	        return new ReviewOrderItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sender = source["sender"];
	        this.messageTime = source["messageTime"];
	        this.messageDate = source["messageDate"];
	        this.orderDate = source["orderDate"];
	        this.reason = source["reason"];
	        this.confidence = source["confidence"];
	        this.rawContent = source["rawContent"];
	        this.orderCount = source["orderCount"];
	        this.orders = this.convertValues(source["orders"], ordertutil.ExceladdressInfo);
	        this.kind = source["kind"];
	        this.actionType = source["actionType"];
	        this.actionStatus = source["actionStatus"];
	        this.matchBy = source["matchBy"];
	        this.changes = source["changes"];
	        this.quote = source["quote"];
	        this.before = this.convertValues(source["before"], ordertutil.ExceladdressInfo);
	        this.after = this.convertValues(source["after"], ordertutil.ExceladdressInfo);
	        this.candidates = source["candidates"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AutoOrderResult {
	    success: boolean;
	    message: string;
	    source: string;
	    exportFrom: string;
	    exportTo: string;
	    exported: number;
	    candidates: number;
	    dropped: number;
	    aiOrders: number;
	    skipped: number;
	    failed: number;
	    workers: number;
	    totalOrders: number;
	    orders: ordertutil.ExceladdressInfo[];
	    targetDate: string;
	    reviewCount: number;
	    reviewOrders: ReviewOrderItem[];
	    details: AutoOrderDetail[];
	    actionCandidates: number;
	    actionCount: number;
	    actionApplied: number;
	    actionFailed: number;
	    actionManual: number;
	    actions: ActionOrderItem[];
	    warnings: string[];
	    configPath: string;
	    newMessages: number;
	    duplicates: number;
	    imported: number;
	    exportedFile: string;
	    trigger: string;
	    autoMode: boolean;
	    nextRunAt: string;
	
	    static createFrom(source: any = {}) {
	        return new AutoOrderResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.source = source["source"];
	        this.exportFrom = source["exportFrom"];
	        this.exportTo = source["exportTo"];
	        this.exported = source["exported"];
	        this.candidates = source["candidates"];
	        this.dropped = source["dropped"];
	        this.aiOrders = source["aiOrders"];
	        this.skipped = source["skipped"];
	        this.failed = source["failed"];
	        this.workers = source["workers"];
	        this.totalOrders = source["totalOrders"];
	        this.orders = this.convertValues(source["orders"], ordertutil.ExceladdressInfo);
	        this.targetDate = source["targetDate"];
	        this.reviewCount = source["reviewCount"];
	        this.reviewOrders = this.convertValues(source["reviewOrders"], ReviewOrderItem);
	        this.details = this.convertValues(source["details"], AutoOrderDetail);
	        this.actionCandidates = source["actionCandidates"];
	        this.actionCount = source["actionCount"];
	        this.actionApplied = source["actionApplied"];
	        this.actionFailed = source["actionFailed"];
	        this.actionManual = source["actionManual"];
	        this.actions = this.convertValues(source["actions"], ActionOrderItem);
	        this.warnings = source["warnings"];
	        this.configPath = source["configPath"];
	        this.newMessages = source["newMessages"];
	        this.duplicates = source["duplicates"];
	        this.imported = source["imported"];
	        this.exportedFile = source["exportedFile"];
	        this.trigger = source["trigger"];
	        this.autoMode = source["autoMode"];
	        this.nextRunAt = source["nextRunAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CheckoutAuditItem {
	    id: string;
	    kind: string;
	    kindLabel: string;
	    reason: string;
	    confidence: string;
	    sender: string;
	    messageTime: string;
	    messageDate: string;
	    orderDate: string;
	    rawContent: string;
	    orderCount: number;
	    orders: ordertutil.ExceladdressInfo[];
	    matchScore: number;
	    matchLabel: string;
	    matchedAt: string;
	    matchOrders: ordertutil.ExceladdressInfo[];
	
	    static createFrom(source: any = {}) {
	        return new CheckoutAuditItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.kindLabel = source["kindLabel"];
	        this.reason = source["reason"];
	        this.confidence = source["confidence"];
	        this.sender = source["sender"];
	        this.messageTime = source["messageTime"];
	        this.messageDate = source["messageDate"];
	        this.orderDate = source["orderDate"];
	        this.rawContent = source["rawContent"];
	        this.orderCount = source["orderCount"];
	        this.orders = this.convertValues(source["orders"], ordertutil.ExceladdressInfo);
	        this.matchScore = source["matchScore"];
	        this.matchLabel = source["matchLabel"];
	        this.matchedAt = source["matchedAt"];
	        this.matchOrders = this.convertValues(source["matchOrders"], ordertutil.ExceladdressInfo);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CheckoutAuditResult {
	    success: boolean;
	    message: string;
	    from: string;
	    to: string;
	    source: string;
	    exported: number;
	    candidates: number;
	    dropped: number;
	    aiFailed: number;
	    checkoutOrders: number;
	    recognizedOrders: number;
	    matched: number;
	    missing: number;
	    uncertain: number;
	    unmatchedBills: number;
	    reviewCount: number;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new CheckoutAuditResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.message = source["message"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.source = source["source"];
	        this.exported = source["exported"];
	        this.candidates = source["candidates"];
	        this.dropped = source["dropped"];
	        this.aiFailed = source["aiFailed"];
	        this.checkoutOrders = source["checkoutOrders"];
	        this.recognizedOrders = source["recognizedOrders"];
	        this.matched = source["matched"];
	        this.missing = source["missing"];
	        this.uncertain = source["uncertain"];
	        this.unmatchedBills = source["unmatchedBills"];
	        this.reviewCount = source["reviewCount"];
	        this.warnings = source["warnings"];
	    }
	}
	export class Config {
	    group: string;
	    senders: string;
	    days: number;
	    dataDir: string;
	    workDir: string;
	    dataKey: string;
	    platform: string;
	    version: number;
	    autoKey: boolean;
	    keyProviderPath: string;
	    providerTimeout: number;
	    autoKeyOnFailure: boolean;
	    wcdbLibPath: string;
	    sessionId: string;
	    concurrency: number;
	    onlyToday: boolean;
	    aiTimeout: number;
	    autoTargetDate: boolean;
	    targetDate: string;
	    useRuleFilter: boolean;
	    autoRun: boolean;
	    runOnStartup: boolean;
	    autoRunInterval: number;
	    incremental: boolean;
	    autoImport: boolean;
	    autoExportExcel: boolean;
	    exportDir: string;
	    configPath?: string;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.group = source["group"];
	        this.senders = source["senders"];
	        this.days = source["days"];
	        this.dataDir = source["dataDir"];
	        this.workDir = source["workDir"];
	        this.dataKey = source["dataKey"];
	        this.platform = source["platform"];
	        this.version = source["version"];
	        this.autoKey = source["autoKey"];
	        this.keyProviderPath = source["keyProviderPath"];
	        this.providerTimeout = source["providerTimeout"];
	        this.autoKeyOnFailure = source["autoKeyOnFailure"];
	        this.wcdbLibPath = source["wcdbLibPath"];
	        this.sessionId = source["sessionId"];
	        this.concurrency = source["concurrency"];
	        this.onlyToday = source["onlyToday"];
	        this.aiTimeout = source["aiTimeout"];
	        this.autoTargetDate = source["autoTargetDate"];
	        this.targetDate = source["targetDate"];
	        this.useRuleFilter = source["useRuleFilter"];
	        this.autoRun = source["autoRun"];
	        this.runOnStartup = source["runOnStartup"];
	        this.autoRunInterval = source["autoRunInterval"];
	        this.incremental = source["incremental"];
	        this.autoImport = source["autoImport"];
	        this.autoExportExcel = source["autoExportExcel"];
	        this.exportDir = source["exportDir"];
	        this.configPath = source["configPath"];
	    }
	}

}

