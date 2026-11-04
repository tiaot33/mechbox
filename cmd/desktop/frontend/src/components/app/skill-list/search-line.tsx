// 搜索行：写出正在搜什么、当前页里有几个结果，点"清除"回到不搜索。列表页放在列表顶上，关系图页放在图上面。
import { useT } from "@/lib/i18n";
import { setSearch, useView } from "@/lib/view";
import { Button } from "@/components/ui/button";

export function SearchLine({ results }: { results: number }) {
  const search = useView((v) => v.search.trim());
  const t = useT().list;
  if (!search) return null;
  return (
    <div className="search-line mb-2.5 flex items-center gap-2 text-[12px] text-muted-foreground">
      <span>{t.searchResults(search, results)}</span>
      <Button variant="link" className="text-[12px]" onClick={() => setSearch("")}>
        {t.clearSearch}
      </Button>
    </div>
  );
}
