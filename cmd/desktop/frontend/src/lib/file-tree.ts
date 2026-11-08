// 按路径把文件排成目录树：技能详情的文件浏览与版本比较的文件列表共用，排法一样。
// 同一层目录在前、文件在后，各自按名字排。

// TreeNode 是目录树里的一项：文件带着 item(调用方的原始数据),目录没有 item、有 children。
export interface TreeNode<T> {
  name: string;
  path: string;
  item?: T;
  children: TreeNode<T>[];
}

// fileTree 把 items 按 path(用 / 分隔)排成目录树。
export function fileTree<T extends { path: string }>(items: T[]): TreeNode<T>[] {
  const root: TreeNode<T> = { name: "", path: "", children: [] };
  for (const item of items) {
    const parts = item.path.split("/");
    let dir = root;
    parts.forEach((name, i) => {
      const path = parts.slice(0, i + 1).join("/");
      if (i === parts.length - 1) {
        dir.children.push({ name, path, item, children: [] });
        return;
      }
      let next = dir.children.find((c) => !c.item && c.name === name);
      if (!next) {
        next = { name, path, children: [] };
        dir.children.push(next);
      }
      dir = next;
    });
  }
  const sort = (nodes: TreeNode<T>[]) => {
    nodes.sort((a, b) => Number(!!a.item) - Number(!!b.item) || a.name.localeCompare(b.name));
    nodes.forEach((n) => sort(n.children));
  };
  sort(root.children);
  return root.children;
}
