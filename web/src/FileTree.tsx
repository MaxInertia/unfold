import { useEffect, useMemo, useState } from "react";
import { fetchFiles } from "./api";

// FileTree is the sidebar's "files" tab body: the indexed source files as a
// collapsible folder tree. Clicking a file opens it in the viewer (as a
// "file:<path>" pseudo-target whose frame is the whole file). The card / tab
// chrome lives in App.
export function FileTree({ onOpen }: { onOpen: (targetId: string) => void }) {
  const [files, setFiles] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());

  useEffect(() => {
    let alive = true;
    fetchFiles()
      .then((f) => alive && setFiles(f))
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
  }, []);

  const root = useMemo(() => (files ? buildTree(files) : null), [files]);

  if (error) return <p className="tree-note tree-note--error">{error}</p>;
  if (!files || !root) return <p className="tree-note">…</p>;
  if (files.length === 0) return <p className="tree-note">no files</p>;

  function toggle(path: string) {
    setCollapsed((c) => {
      const n = new Set(c);
      if (n.has(path)) n.delete(path);
      else n.add(path);
      return n;
    });
  }

  return (
    <ul className="tree-list tree-list--root">
      <DirChildren node={root} collapsed={collapsed} toggle={toggle} onOpen={onOpen} />
    </ul>
  );
}

interface TreeNode {
  name: string;
  path: string; // display path (relative)
  isFile: boolean;
  targetId?: string; // for files: "file:<absolute path>"
  children: Map<string, TreeNode>;
}

function DirChildren({
  node,
  collapsed,
  toggle,
  onOpen,
}: {
  node: TreeNode;
  collapsed: Set<string>;
  toggle: (path: string) => void;
  onOpen: (targetId: string) => void;
}) {
  // Folders first, then files, each alphabetical.
  const kids = [...node.children.values()].sort((a, b) => {
    if (a.isFile !== b.isFile) return a.isFile ? 1 : -1;
    return a.name < b.name ? -1 : a.name > b.name ? 1 : 0;
  });
  return (
    <>
      {kids.map((k) =>
        k.isFile ? (
          <li key={k.path} className="tree-node">
            <div
              className="tree-row tree-row--expandable file-row"
              onClick={() => k.targetId && onOpen(k.targetId)}
              title={k.path}
            >
              <span className="tree-twisty">·</span>
              <span className="tree-label">{k.name}</span>
            </div>
          </li>
        ) : (
          <li key={k.path} className="tree-node">
            <div
              className="tree-row tree-row--expandable"
              onClick={() => toggle(k.path)}
              title={k.path}
            >
              <span className="tree-twisty">{collapsed.has(k.path) ? "▸" : "▾"}</span>
              <span className="tree-label dir-label">{k.name}/</span>
            </div>
            {!collapsed.has(k.path) && (
              <ul className="tree-list">
                <DirChildren node={k} collapsed={collapsed} toggle={toggle} onOpen={onOpen} />
              </ul>
            )}
          </li>
        ),
      )}
    </>
  );
}

// Build a folder tree from absolute file paths, stripping the longest common
// directory prefix so the tree starts at the project root.
function buildTree(files: string[]): TreeNode {
  const root: TreeNode = { name: "", path: "", isFile: false, children: new Map() };
  const split = files.map((f) => f.split("/"));
  let common = split[0].slice(0, -1);
  for (const s of split) {
    const dir = s.slice(0, -1);
    let k = 0;
    while (k < common.length && k < dir.length && common[k] === dir[k]) k++;
    common = common.slice(0, k);
  }
  const prefix = common.length;

  files.forEach((full, i) => {
    const parts = split[i].slice(prefix);
    let cur = root;
    let acc = "";
    parts.forEach((p, j) => {
      acc = acc ? `${acc}/${p}` : p;
      const isFile = j === parts.length - 1;
      let child = cur.children.get(p);
      if (!child) {
        child = {
          name: p,
          path: acc,
          isFile,
          targetId: isFile ? `file:${full}` : undefined,
          children: new Map(),
        };
        cur.children.set(p, child);
      }
      cur = child;
    });
  });
  return root;
}
