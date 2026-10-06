// Package library 用 SQLite 数据库统一管理 Agent Skills:先对来源做入库预览再入库,然后把 skill 安装到
// Claude Code 与 Codex 在各作用域读取的 skills 目录。数据库是唯一权威数据源:
// Agent 目录里的技能文件夹都在安装时从库写出;在那里做的修改不会进库,巡检只拿它们与库里的版本比对。
//
// 单说"技能"(Skill)指的都是库里的技能;市场搜到的是 SearchSkill,入库预览里的是 Candidate,
// 装出去的是 SkillScope。
//
// 安装与安装计划方法都只接受 Scope 身份:Agent 目录由 Resolve 算出,记录按身份落库,
// 未知 Agent 或未登记项目返回 NotFound。Agent 目录变了之后只认新目录,旧目录里的文件夹不管。
package library
