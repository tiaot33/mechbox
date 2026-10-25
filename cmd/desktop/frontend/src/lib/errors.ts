// 把技能库的错误码翻成界面文案。技能库的 message 是英文、面向开发者，界面契约是码 + Details
// (见 internal/library/errors.go 的 ErrorCode 注释);message 只作为次要信息附在文案后面。
import { ErrorCode } from "@bindings/internal/library";
import { messages } from "./i18n";
import type { LibraryError } from "./library";

function codeText(code: string): string | undefined {
  const m = messages().errors;
  return {
    [ErrorCode.InvalidArgument]: m.invalidArgument,
    [ErrorCode.NotFound]: m.notFound,
    [ErrorCode.AlreadyExists]: m.alreadyExists,
    [ErrorCode.Conflict]: m.conflict,
    [ErrorCode.FailedPrecondition]: m.failedPrecondition,
    [ErrorCode.Canceled]: m.canceled,
    [ErrorCode.SourceUnreachable]: m.sourceUnreachable,
    [ErrorCode.SourceInvalid]: m.sourceInvalid,
    [ErrorCode.Busy]: m.busy,
    [ErrorCode.Permission]: m.permission,
    [ErrorCode.NotConfigured]: m.notConfigured,
    [ErrorCode.Internal]: m.internal,
  }[code];
}

// describeError 返回一行主文案 + 一行细节(Details 里的名字 / 路径与原始 message)。
export function describeError(err: LibraryError): string {
  const head = codeText(err.code) ?? messages().errors.failed;
  const d = (err.details ?? {}) as Record<string, unknown>; // 名字 / 路径 / 字段名，有哪个显示哪个
  const facts = [d.name, d.path, d.field].filter((x) => typeof x === "string" && x) as string[];
  const detail = [facts.join(" · "), err.message].filter(Boolean).join("\n");
  return detail ? `${head}\n${detail}` : head;
}

// unmanagedSourceError 说明带来源入库为什么没成，以及用户接下来能做什么。
export function unmanagedSourceError(err: LibraryError, name: string): string {
  const m = messages().errors;
  const clear = m.sourceClear;
  const field = err.details?.field;
  switch (err.code) {
    case ErrorCode.AlreadyExists:
      return m.sourceExists(err.details?.name ?? name);
    case ErrorCode.SourceUnreachable:
      return m.sourceUnreachableRetry(clear);
    case ErrorCode.NotFound:
      // 找不到的可能是技能，也可能是分支或子目录:带上原因。
      if (err.details?.name) return m.sourceMissing(err.details.name, clear, err.message);
      break;
    case ErrorCode.InvalidArgument:
      if (field === "source_input" || field === "source_ref") return m.sourceUnrecognized(clear, err.message);
      break;
  }
  return describeError(err);
}

// aiError 说明调用 AI(测试连接、翻译描述)为什么没成、该查哪一项，后面附上服务给出的原因：通用文案会把 AI 服务说成「来源」。
// 回答被 max_tokens 截断时说明去请求体里调大 max_tokens;请求头、请求体以外的字段错误与其他错误码返回 undefined,交给 describeError。
export function aiError(err: LibraryError): string | undefined {
  const t = messages().credentials;
  const field = err.details?.field;
  const heads: Record<string, string | undefined> = field
    ? { headers: t.invalidHeaders, body: t.invalidBody, max_tokens: t.truncated }
    : {
        [ErrorCode.SourceUnreachable]: t.testUnreachable,
        [ErrorCode.NotFound]: t.testNotFound,
        [ErrorCode.Permission]: t.testPermission,
        [ErrorCode.InvalidArgument]: t.testRejected,
        [ErrorCode.NotConfigured]: t.aiNotConfigured,
      };
  const head = heads[field || err.code];
  return head && `${head}\n${err.message}`;
}
