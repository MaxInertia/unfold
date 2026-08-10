import { useEffect, useMemo, useState } from "react";
import { fetchChannels } from "./api";
import type { Channel, ChannelEnd } from "./types";

// Every key in the workspace, with who sends and who receives.
//
// The platform graph answers "what talks to what". This answers the question
// underneath it — "what is this event, and who is on it" — which otherwise
// meant finding a call site that mentions the key and reading outward from
// there. It reads the join rather than any index, so it is complete for
// whatever the workspace knows, however little has been opened.
//
// Deliberately a list of names, not of functions: which *function* subscribes
// is a per-service question that costs an index, and following one end is what
// the boundary card is for.
export function ChannelIndex({
  onSelectService,
}: {
  // Picking a service here selects it at the platform level, which is the
  // same gesture as clicking it in the graph.
  onSelectService: (alias: string) => void;
}) {
  const [channels, setChannels] = useState<Channel[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState("");

  useEffect(() => {
    let alive = true;
    fetchChannels()
      .then((cs) => alive && setChannels(cs))
      .catch((e: Error) => alive && setError(e.message));
    return () => {
      alive = false;
    };
  }, []);

  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q || !channels) return channels ?? [];
    // Matching on the ends too, because "which events does orders touch" is
    // the same question asked from the other side.
    return channels.filter(
      (c) =>
        c.key.toLowerCase().includes(q) ||
        c.channel.toLowerCase().includes(q) ||
        [...c.inbound, ...c.outbound].some((e) => e.service.toLowerCase().includes(q)),
    );
  }, [channels, filter]);

  if (error) return <div className="channels-note channels-note--error">{error}</div>;
  if (!channels) return <div className="channels-note">loading…</div>;
  if (channels.length === 0) {
    return <div className="channels-note">no keys recognized in this workspace</div>;
  }

  return (
    <div className="channels">
      <div className="channels-head">
        <input
          className="channels-filter"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder="filter by key or service"
          aria-label="filter channels"
        />
        <span className="channels-count">
          {shown.length === channels.length
            ? `${channels.length} keys`
            : `${shown.length} of ${channels.length}`}
        </span>
      </div>
      <ul className="channels-list">
        {shown.map((c) => (
          <li key={`${c.channel}:${c.key}`} className="channel">
            <div className="channel-key">
              <span className="channel-key-name">{c.key}</span>
              <span className="channel-kind">{c.channel}</span>
            </div>
            <div className="channel-ends">
              <Side label="emits" ends={c.outbound} onSelect={onSelectService} />
              <span className="channel-arrow" aria-hidden="true">
                →
              </span>
              <Side label="receives" ends={c.inbound} onSelect={onSelectService} />
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}

// One side of one key. An empty side is stated rather than left blank: a topic
// nobody subscribes to is a finding, and a gap that looks like a rendering
// slip is not.
function Side({
  label,
  ends,
  onSelect,
}: {
  label: string;
  ends: ChannelEnd[];
  onSelect: (alias: string) => void;
}) {
  if (ends.length === 0) {
    return (
      <span className="channel-side channel-side--empty" title={`nothing ${label} this key`}>
        no one
      </span>
    );
  }
  return (
    <span className="channel-side">
      {ends.map((e) => (
        <button
          key={e.repo}
          type="button"
          className={`channel-service${e.indexed ? "" : " channel-service--unindexed"}`}
          onClick={() => onSelect(e.repo)}
          title={
            e.indexed
              ? `${e.service} ${label} this key`
              : `${e.service} ${label} this key — its code hasn't been indexed, so there's nothing to open yet`
          }
        >
          {e.service}
        </button>
      ))}
    </span>
  );
}
