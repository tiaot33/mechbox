// 技能详情里的版本历史：与当前版本比较(打开比较弹窗看逐行差异),查看旧版本的文件。
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { SkillVersion, VersionID } from "@bindings/internal/library";
import { compareVersions } from "@/features/update/version-compare";
import { formatDate } from "@/lib/content";
import { describeError } from "@/lib/errors";
import { useT } from "@/lib/i18n";
import { toLibraryError, versionFilesQuery } from "@/lib/library";
import { cn } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { NativeSelect } from "@/components/ui/native-select";
import { Notice } from "@/components/ui/notice";
import { Code, useFileContent } from "./files-card";

// VersionsCard 列出版本历史(最新在前);可查看某个版本的文件，或与当前版本比较(打开比较弹窗)。
// 版本按这个技能入库以来的先后编号(版本 1 是入库时的那一份),不用库里全局的版本 ID。
export function VersionsCard({ name, versions, currentID }: { name: string; versions: SkillVersion[]; currentID: VersionID }) {
  const tt = useT();
  const t = tt.drawer;
  const number = new Map(versions.map((v, i) => [v.id, i + 1]));
  const list = [...versions].reverse();
  // 正在看哪个版本的文件：文件列表按版本 ID 查询，界面上总是最后点的那个版本，收起后晚返回的读取也不会再展开。
  const [viewing, setViewing] = useState<VersionID | null>(null);
  const [chosenPath, setViewPath] = useState("");
  const viewed = useQuery({ ...versionFilesQuery(viewing ?? 0), enabled: viewing !== null });
  const files = viewed.data ?? [];
  const viewPath = files.some((f) => f.path === chosenPath) ? chosenPath : (files[0]?.path ?? "");
  const viewedText = useFileContent(viewing ?? undefined, viewPath);

  const compare = (v: SkillVersion, origin: Element) =>
    compareVersions({ title: t.compareTitle(name, number.get(v.id)), from: v.id, to: currentID, origin });
  const view = (v: SkillVersion) => {
    setViewing(v.id);
    setViewPath("");
  };

  const notes = (v: SkillVersion) => [v.based_on ? t.basedOn(number.get(v.based_on)) : "", v.maybe_edited ? t.maybeEdited : ""].filter(Boolean);

  return (
    <Card>
      <CardHeader title={t.versions} note={t.versionsNote(versions.length)} />
      {/* 各行按列对齐：每行固定六格，没有的留空，"当前"和"与当前比较"占同一格。 */}
      <ol className="versions grid grid-cols-[auto_auto_auto_minmax(0,1fr)_auto_auto] gap-x-3 gap-y-1.5">
        {list.map((v) => (
          <li key={v.id} className="col-span-full grid grid-cols-subgrid items-center text-body">
            <span className="font-semibold">{t.versionN(number.get(v.id))}</span>
            <span>{formatDate(v.created_at)}</span>
            {/* 用目录里的修改更新进来的版本沿用所基于版本的来源版本，再写一遍像是没变：不写，说明占这两格。 */}
            {!v.based_on && <span className="font-mono text-caption text-muted-foreground">{v.revision?.slice(0, 12)}</span>}
            {/* 放不下时截断，悬停看全。 */}
            <span className={cn("truncate text-caption text-muted-foreground", v.based_on && "col-span-2")} title={notes(v).join(" ")}>
              {notes(v).join(" ")}
            </span>
            {v.id === currentID ? (
              <Badge className="justify-self-start">{t.current}</Badge>
            ) : (
              <Button variant="ghost" size="inline" className="justify-self-start" onClick={(e) => void compare(v, e.currentTarget)}>
                {t.compare}
              </Button>
            )}
            <Button variant="ghost" size="inline" onClick={() => view(v)}>
              {t.viewFiles}
            </Button>
          </li>
        ))}
      </ol>
      {viewing !== null && (
        <div className="mt-2 flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <b className="text-caption">{t.versionFiles(number.get(viewing))}</b>
            <NativeSelect value={viewPath} onChange={(e) => setViewPath(e.target.value)}>
              {files.map((f) => (
                <option key={f.path} value={f.path}>
                  {f.path}
                </option>
              ))}
            </NativeSelect>
            <span className="grow" />
            <Button variant="ghost" size="inline" onClick={() => setViewing(null)}>
              {t.collapse}
            </Button>
          </div>
          {viewed.isError ? (
            <Notice alert>{describeError(toLibraryError(viewed.error))}</Notice>
          ) : viewedText === null ? (
            <Notice>{tt.common.binary}</Notice>
          ) : viewedText !== undefined ? (
            <Code>{viewedText}</Code>
          ) : null}
        </div>
      )}
    </Card>
  );
}
