import { fromSnapshot, type Kind, type State } from './graph'
declare global {
  interface Window {go:{main:{App:{Attach(attempt:string):Promise<string>;ReadTerminal(id:string):Promise<string>;
    WriteTerminal(id:string,data:string):Promise<void>;ResizeTerminal(id:string,cols:number,rows:number):Promise<void>;
    Detach(id:string):Promise<void>;TermScroll(id:string,rows:number):Promise<void>;Clipboard(text:string):Promise<boolean>;
    Request(action:string,payload:string):Promise<string>,OpenedFile():Promise<string>}}}}
}
export async function request<T>(action:string, payload=''):Promise<T> {
  return JSON.parse(await window.go.main.App.Request(action,payload))
}
export interface FlowNode {
  id:string; name:string; kind:string; status:string; run_id:string; parent_id?:string; attempt_id?:string;
  wait?:{kind:string;ref?:string}
}
export interface Workspace {
  cursor:number; nodes:FlowNode[]|null; edges:{from_id:string;to_id:string;milestone?:string}[]|null;
  attempts:{id:string;node_id:string;number:number;status:string}[]|null
}
const kinds:Record<string,Kind>={workflow:'group',group:'group',session:'session',harness:'session',tool:'step',watch:'watch'}
const states:Record<string,State>={waiting_for_dependencies:'pending',ready:'pending',running:'running',
  waiting_for_input:'blocked',stopping:'stopped',succeeded:'done',failed:'failed',interrupted:'blocked',skipped:'stopped',cancelled:'stopped'}
export interface LiveSession {session_id:string;attempt_id:string;status:string;command:string;work_dir:string;child:{pid:number}}
export function graphOf(w:Workspace, live:LiveSession[] = []) {
  const attemptNodes=new Map((w.attempts??[]).map(a=>[a.id,a.node_id]))
  return fromSnapshot({seq:w.cursor,sessions:live.flatMap(s=>{const node=attemptNodes.get(s.attempt_id);if(!node)return[];const agent=s.command.split('/').pop()??'harness';return[{id:s.session_id,attempt:s.attempt_id,node,agent,mode:'pty' as const,dir:s.work_dir,pid:s.child?.pid,started:'',tag:s.status}]}),nodes:(w.nodes??[]).map(n=>({
    id:n.id,title:n.name||n.id,kind:kinds[n.kind]??'step',state:states[n.status]??'pending',
    label:n.kind+' · '+n.status.replaceAll('_',' '),parent:n.parent_id,created:'',updated:''})),
    edges:(w.edges??[]).map(e=>({from:e.from_id,to:e.to_id,reason:e.milestone}))})
}
