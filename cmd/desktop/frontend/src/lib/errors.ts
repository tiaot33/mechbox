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

// unmanagedSourceError 说明带来源入库为什么没成，以及用户接下来能做什么。
export function unmanagedSourceError(err: LibraryError, name: string): string {
  const clear = "也可以清空来源链接，作为没有来源的技能入库。";
  const field = err.details?.field;
  switch (err.code) {
    case ErrorCode.AlreadyExists:
      return `库里已有这个来源的「${err.details?.name ?? name}」，这里的内容和它的哪个版本都不一样，所以不合并。可以清空来源链接，作为没有来源的技能入库，或者先不入库。`;
    case ErrorCode.SourceUnreachable:
      return `连不上这个来源，可以稍后重试或改链接。${clear}`;
    case ErrorCode.NotFound:
      // 找不到的可能是技能，也可能是分支或子目录:带上原因。
      if (err.details?.name) return `来源里找不到「${err.details.name}」，可以改链接。${clear}\n${err.message}`;
      break;
    case ErrorCode.InvalidArgument:
      if (field === "source_input" || field === "source_ref") return `认不出这个来源链接，可以改一下。${clear}\n${err.message}`;
      break;
  }
  return describeError(err);
}
