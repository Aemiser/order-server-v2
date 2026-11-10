export interface Order {
  id: string;
  name: string;
  phone: string;
  address: string;
  items: string;
  note: string;
}

export interface ReviewOrderFields {
  VID?: number;
  '收件人'?: string;
  '手机号'?: string;
  '收货地址'?: string;
  '商品规格'?: string;
  '备注'?: string;
}

// 「待人工审查」的一条数据：一条原始聊天消息 + AI 从中识别出的若干笔订单。
// 没有日期标记 / 日期与目标日期不符 / 判定存疑的订单会停在这里；
// 订单操作层处理过的订单也会停在这里（kind='action'），带操作前后对照，
// 「加入预览区」= 采用操作结果，「忽略」= 撤销操作并恢复原始订单。
export interface ReviewOrder {
  id: string;
  sender: string;
  messageTime: string;
  messageDate: string;
  orderDate: string;
  reason: string;
  confidence: string;
  rawContent: string;
  orderCount: number;
  orders: ReviewOrderFields[];
  // 以下字段仅订单操作项有值
  kind?: string;          // action = 订单操作
  actionType?: string;    // delete / modify
  actionStatus?: string;  // applied / manual / ambiguous / unmatched
  matchBy?: string;       // 定位方式
  changes?: string;       // 变更说明
  quote?: string;         // 被引用的订单原文
  before?: ReviewOrderFields[];
  after?: ReviewOrderFields[];
  candidates?: string[];
}

// 订单操作自定义术语规则：把群内口语 / 缩写映射到删除 / 修改 / 排除。
// keyword 用 / 分隔多个别名；字段含义见后端 models.CfgOrderActionRule。
export interface OrderActionRule {
  id: number;
  keyword: string;
  action: string;    // delete / modify / ignore
  field: string;     // address / phone / recipient / remarks / specs
  valueRule: string; // none / after / phone / append_rest
  scope: string;     // both / trigger / prompt
  priority: number;
  enabled: boolean;
  sortOrder: number;
  remark: string;
}

// 声明window.go对象的类型
declare global {
  interface Window {
    go?: {
      main?: {
        App?: {
          OrderRecognition?: (context: string) => Promise<any>;
          [key: string]: any;
        };
        [key: string]: any;
      };
      [key: string]: any;
    };
  }
}
