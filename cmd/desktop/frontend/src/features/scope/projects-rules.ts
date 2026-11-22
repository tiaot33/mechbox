// 注销项目(projects.tsx)里的判断:选「同时删除」时会删掉什么。不碰界面、不调技能库，单元测试见 projects-rules.test.ts。
import type { ProjectID } from "@bindings/internal/library";
import { staleLinksOf } from "@/lib/consent";
import type { Snapshot } from "@/lib/library";

// unregisterDeletes 是注销项目、选了同时删除时会删掉的:places 是装在这个项目里的各处(文件夹),
// links 是技能库一并删掉的旧链接(项目里别的技能目录指向这些文件夹、又没有记录的符号链接),确认框要逐个写出。
export function unregisterDeletes(s: Snapshot, project: ProjectID) {
  const places = s.skillScopes.filter((p) => p.scope.project_id === project);
  return { places, links: staleLinksOf(places) };
}
