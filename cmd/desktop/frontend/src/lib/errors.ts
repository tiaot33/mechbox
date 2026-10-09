// 把技能库的错误码翻成界面文案。技能库的 message 是英文、面向开发者，界面契约是码 + Details
// (见 internal/library/errors.go 的 ErrorCode 注释);message 只作为次要信息附在文案后面。
import { ErrorCode } from "@bindings/internal/library";
import type { LibraryError } from "./library";

const codeText: Record<string, string> = {
  [ErrorCode.InvalidArgument]: "输入无效",
  [ErrorCode.NotFound]: "目标不存在",
  [ErrorCode.AlreadyExists]: "已经存在",
  [ErrorCode.Conflict]: "状态已变化，请重新操作",
  [ErrorCode.FailedPrecondition]: "当前状态不允许这个操作",
  [ErrorCode.Canceled]: "已取消",
  [ErrorCode.SourceUnreachable]: "来源暂时无法访问，可以稍后重试",
  [ErrorCode.SourceInvalid]: "来源内容不可用，建议换一个来源",
  [ErrorCode.Busy]: "另一个进程正在写技能库，请稍后重试",
  [ErrorCode.Permission]: "权限不足或凭证被拒绝",
  [ErrorCode.NotConfigured]: "缺少所需配置(见设置)",
  [ErrorCode.Internal]: "内部错误",
};

// describeError 返回一行主文案 + 一行细节(Details 里的名字 / 路径与原始 message)。
export function describeError(err: LibraryError): string {
  const head = codeText[err.code] ?? "操作失败";
  const d = (err.details ?? {}) as Record<string, unknown>; // 名字 / 路径 / 字段名，有哪个显示哪个
  const facts = [d.name, d.path, d.field].filter((x) => typeof x === "string" && x) as string[];
  const detail = [facts.join(" · "), err.message].filter(Boolean).join("\n");
  return detail ? `${head}\n${detail}` : head;
}
