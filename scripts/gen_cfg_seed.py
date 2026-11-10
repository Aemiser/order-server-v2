# -*- coding: utf-8 -*-
"""校验 go/cfgseed/cfg_seed.json 并生成 scripts/cfg_init.sql。

cfg_seed.json 是 cfg_* 配置表初始数据的唯一来源（内嵌进 Go 二进制）。
迁移阶段的一次性提取逻辑已完成：settings.yaml 的非 log/db 段、aimap.json、
productbelong.json、systemPrompt.txt 以及代码里的 AI 提示词常量都已并入该文件。

日常维护：
  - 改数据库里的配置 / 提示词 -> 直接改 cfg_* 表，运行时立即生效；
  - 改「新环境的初始默认值」-> 编辑 cfg_seed.json 后运行本脚本重新生成 SQL。
"""
import json
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SEED = os.path.join(ROOT, "go", "cfgseed", "cfg_seed.json")
SQL = os.path.join(ROOT, "scripts", "cfg_init.sql")

DDL = """-- order_server cfg_* 配置表初始化脚本
-- 由 scripts/gen_cfg_seed.py 从 go/cfgseed/cfg_seed.json 自动生成，请勿手工编辑。
-- 程序每次启动会自动建表并在空表时灌入种子；本脚本用于新环境手工部署 / 备份。
-- INSERT IGNORE：重复执行安全，且不会覆盖数据库中已修改过的配置。

CREATE TABLE IF NOT EXISTS `cfg_settings` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `cfg_group` varchar(64) NOT NULL,
  `cfg_key` varchar(128) NOT NULL,
  `cfg_value` text,
  `value_type` varchar(16) DEFAULT 'string',
  `remark` varchar(255) DEFAULT NULL,
  `enabled` tinyint(1) DEFAULT '1',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_cfg_setting` (`cfg_group`,`cfg_key`),
  KEY `idx_cfg_settings_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS `cfg_ai_models` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `code` varchar(64) NOT NULL,
  `label` varchar(64) NOT NULL,
  `name` varchar(64) DEFAULT NULL,
  `model_type` bigint NOT NULL,
  `api_key` varchar(255) DEFAULT NULL,
  `api_url` varchar(255) DEFAULT NULL,
  `model_name` varchar(64) DEFAULT NULL,
  `timeout` bigint DEFAULT '600',
  `enabled` tinyint(1) DEFAULT '1',
  `sort_order` bigint DEFAULT '0',
  `remark` varchar(255) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_cfg_ai_model_code` (`code`),
  KEY `idx_cfg_ai_models_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS `cfg_product_belongs` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `category` varchar(64) NOT NULL,
  `aliases` text,
  `sort_order` bigint DEFAULT '0',
  `enabled` tinyint(1) DEFAULT '1',
  `remark` varchar(255) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_cfg_product_category` (`category`),
  KEY `idx_cfg_product_belongs_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS `cfg_prompts` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `code` varchar(64) NOT NULL,
  `name` varchar(128) DEFAULT NULL,
  `scene` varchar(64) DEFAULT NULL,
  `content` longtext,
  `is_template` tinyint(1) DEFAULT '0',
  `enabled` tinyint(1) DEFAULT '1',
  `sort_order` bigint DEFAULT '0',
  `remark` varchar(255) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_cfg_prompt_code` (`code`),
  KEY `idx_cfg_prompts_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE IF NOT EXISTS `cfg_order_action_rules` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  `keyword` varchar(128) NOT NULL,
  `action_type` varchar(16) NOT NULL,
  `field_name` varchar(32) DEFAULT NULL,
  `value_rule` varchar(32) DEFAULT NULL,
  `scope` varchar(16) DEFAULT 'both',
  `priority` bigint DEFAULT '0',
  `enabled` tinyint(1) DEFAULT '1',
  `sort_order` bigint DEFAULT '0',
  `remark` varchar(255) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_cfg_action_rule_keyword` (`keyword`),
  KEY `idx_cfg_order_action_rules_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

"""


def esc(v):
    """MySQL 字符串字面量转义；提示词里含字面量 \\n，不能被解析成换行。"""
    s = str(v)
    s = s.replace("\\", "\\\\").replace("'", "\\'")
    s = s.replace("\n", "\\n").replace("\r", "\\r").replace("\t", "\\t")
    return "'" + s + "'"


def b(v):
    return "1" if v else "0"


def validate(seed):
    required = ("order_extract", "wechat_order_base", "wechat_full_scan",
                "wechat_target_date", "wechat_order_action")
    codes = {p["code"] for p in seed["prompts"]}
    missing = [c for c in required if c not in codes]
    if missing:
        raise RuntimeError("cfg_seed.json 缺少提示词: %s" % ", ".join(missing))
    for p in seed["prompts"]:
        if not p["content"].strip():
            raise RuntimeError("提示词 %s 内容为空" % p["code"])
    if not seed["aiModels"] or not seed["productBelongs"] or not seed["settings"]:
        raise RuntimeError("cfg_seed.json 的配置段不完整")
    if not seed.get("actionRules"):
        raise RuntimeError("cfg_seed.json 缺少订单操作术语规则 actionRules")
    actions = {"delete", "modify", "ignore"}
    fields = {"address", "phone", "recipient", "remarks", "specs"}
    value_rules = {"none", "after", "phone", "append_rest"}
    scopes = {"both", "trigger", "prompt"}
    for r in seed["actionRules"]:
        kw = r["keyword"].strip()
        if not kw:
            raise RuntimeError("存在空的订单操作术语关键词")
        if r["action"] not in actions:
            raise RuntimeError("术语 %s 的 action 非法: %s" % (kw, r["action"]))
        if r["action"] == "modify" and r["field"] not in fields:
            raise RuntimeError("术语 %s 的 field 非法: %s" % (kw, r["field"]))
        if r["valueRule"] not in value_rules:
            raise RuntimeError("术语 %s 的 valueRule 非法: %s" % (kw, r["valueRule"]))
        if r["scope"] not in scopes:
            raise RuntimeError("术语 %s 的 scope 非法: %s" % (kw, r["scope"]))


def build_sql(seed):
    parts = [DDL, "-- cfg_settings：settings.yaml 中除 log/db 外的通用配置\n"]
    for s in seed["settings"]:
        parts.append(
            "INSERT IGNORE INTO `cfg_settings` "
            "(`cfg_group`,`cfg_key`,`cfg_value`,`value_type`,`remark`,`enabled`) VALUES (%s,%s,%s,%s,%s,%s);\n"
            % (esc(s["group"]), esc(s["key"]), esc(s["value"]),
               esc(s["type"]), esc(s.get("remark", "")), b(s["enabled"]))
        )
    parts.append("\n-- cfg_ai_models：原 aimap.json + settings.yaml(aimodel) + 代码里写死的 URL/模型名\n")
    for m in seed["aiModels"]:
        parts.append(
            "INSERT IGNORE INTO `cfg_ai_models` "
            "(`code`,`label`,`name`,`model_type`,`api_key`,`api_url`,`model_name`,`timeout`,`enabled`,`sort_order`,`remark`) "
            "VALUES (%s,%s,%s,%d,%s,%s,%s,%d,%s,%d,%s);\n"
            % (esc(m["code"]), esc(m["label"]), esc(m["name"]), m["modelType"],
               esc(m["apiKey"]), esc(m["apiUrl"]), esc(m["modelName"]), m["timeout"],
               b(m["enabled"]), m["sortOrder"], esc(m.get("remark", "")))
        )
    parts.append("\n-- cfg_product_belongs：原 productbelong.json\n")
    for p in seed["productBelongs"]:
        parts.append(
            "INSERT IGNORE INTO `cfg_product_belongs` "
            "(`category`,`aliases`,`sort_order`,`enabled`,`remark`) VALUES (%s,%s,%d,%s,%s);\n"
            % (esc(p["category"]), esc(json.dumps(p["aliases"], ensure_ascii=False)),
               p["sortOrder"], b(p["enabled"]), esc(p.get("remark", "")))
        )
    parts.append("\n-- cfg_prompts：全部 AI 提示词（改提示词请直接改这张表）\n")
    for p in seed["prompts"]:
        parts.append(
            "INSERT IGNORE INTO `cfg_prompts` "
            "(`code`,`name`,`scene`,`content`,`is_template`,`enabled`,`sort_order`,`remark`) "
            "VALUES (%s,%s,%s,%s,%s,%s,%d,%s);\n"
            % (esc(p["code"]), esc(p["name"]), esc(p["scene"]), esc(p["content"]),
               b(p["isTemplate"]), b(p["enabled"]), p["sortOrder"], esc(p.get("remark", "")))
        )
    parts.append("\n-- cfg_order_action_rules：订单操作自定义术语规则（口语 -> 删除 / 修改）\n")
    for r in seed["actionRules"]:
        parts.append(
            "INSERT IGNORE INTO `cfg_order_action_rules` "
            "(`keyword`,`action_type`,`field_name`,`value_rule`,`scope`,`priority`,`enabled`,`sort_order`,`remark`) "
            "VALUES (%s,%s,%s,%s,%s,%d,%s,%d,%s);\n"
            % (esc(r["keyword"]), esc(r["action"]), esc(r["field"]), esc(r["valueRule"]),
               esc(r["scope"]), r["priority"], b(r["enabled"]), r["sortOrder"],
               esc(r.get("remark", "")))
        )
    return "".join(parts)


def main():
    with open(SEED, "r", encoding="utf-8") as f:
        seed = json.load(f)
    validate(seed)
    with open(SQL, "w", encoding="utf-8", newline="\n") as f:
        f.write(build_sql(seed))
    print("校验通过，已生成 %s" % SQL)
    print("  settings=%d aiModels=%d productBelongs=%d prompts=%d actionRules=%d" % (
        len(seed["settings"]), len(seed["aiModels"]),
        len(seed["productBelongs"]), len(seed["prompts"]), len(seed["actionRules"])))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001
        print("生成失败: %s" % e, file=sys.stderr)
        sys.exit(1)
