// The client's picture of the daemon: a snapshot, then the events after it. The daemon owns the
// state; this only applies what it is told.

export type Kind = 'session' | 'step' | 'watch' | 'group'
export type State = 'pending' | 'starting' | 'running' | 'watching' | 'blocked' | 'done' | 'failed' | 'stopped'

export interface Node {
  id: string
  kind: Kind
  title: string
  named?: boolean // a person gave the title
  state: State
  label?: string
  detail?: string
  questionId?: string
  parent?: string
  flow?: string
  created: string
  updated: string
}

export interface Edge {
  from: string
  to: string
  reason?: string
}

export interface Session {
  id: string
  attempt?: string
  node: string
  agent: string
  mode: 'pty' | 'pipe'
  tag?: string
  dir: string
  pid?: number
  exit?: number
  started: string
  ended?: string
}

export interface Snapshot {
  seq: number
  nodes: Node[]
  edges: Edge[]
  sessions: Session[]
}

export interface Event {
  seq: number
  type: string
  node?: Node
  id?: string
  edge?: Edge
  session?: Session
}

export interface Graph {
  seq: number
  nodes: Map<string, Node>
  edges: Edge[]
  sessions: Map<string, Session>
}

export function empty(): Graph {
  return { seq: 0, nodes: new Map(), edges: [], sessions: new Map() }
}

export function fromSnapshot(s: Snapshot): Graph {
  return {
    seq: s.seq,
    nodes: new Map(s.nodes.map((n) => [n.id, n])),
    edges: [...s.edges],
    sessions: new Map(s.sessions.map((x) => [x.id, x])),
  }
}

const sameEdge = (a: Edge, b: Edge) => a.from === b.from && a.to === b.to

// apply returns the graph after one event. An event the graph has seen (seq not after its seq) or
// does not know changes nothing.
export function apply(g: Graph, e: Event): Graph {
  if (e.seq <= g.seq) return g
  const next: Graph = { seq: e.seq, nodes: g.nodes, edges: g.edges, sessions: g.sessions }
  switch (e.type) {
    case 'node.created':
    case 'node.updated':
      if (e.node) next.nodes = new Map(g.nodes).set(e.node.id, e.node)
      break
    case 'node.removed':
      if (e.id) {
        const id = e.id
        next.nodes = new Map(g.nodes)
        next.nodes.delete(id)
        next.edges = g.edges.filter((x) => x.from !== id && x.to !== id)
        next.sessions = new Map([...g.sessions].filter(([, s]) => s.node !== id))
      }
      break
    case 'edge.added':
      if (e.edge) next.edges = [...g.edges.filter((x) => !sameEdge(x, e.edge!)), e.edge]
      break
    case 'edge.removed':
      if (e.edge) next.edges = g.edges.filter((x) => !sameEdge(x, e.edge!))
      break
    case 'session':
      if (e.session) next.sessions = new Map(g.sessions).set(e.session.id, e.session)
      break
  }
  return next
}

// children lists a node's children; roots are the nodes without a parent.
export function children(g: Graph, parent: string | undefined): Node[] {
  return [...g.nodes.values()].filter((n) => (n.parent ?? '') === (parent ?? ''))
}

// sessionsOf lists a node's sessions, oldest first.
export function sessionsOf(g: Graph, id: string): Session[] {
  return [...g.sessions.values()].filter((s) => s.node === id).sort((a, b) => a.started.localeCompare(b.started))
}
