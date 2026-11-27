import { fromSnapshot, type Kind, type State } from './graph'
declare global {
  interface Window {runtime:{EventsOn(event:string,callback:(data:any)=>void):()=>void};go:{main:{App:{Follow():void;OpenPreview(attempt:string):Promise<string>;Attach(attempt:string):Promise<string>;AttachScreen(attempt:string):Promise<string>;ReadTerminal(id:string):Promise<string>;StreamTerminal(id:string):Promise<void>;
    OpenNativeTerminal?(attempt:string,x:number,y:number,width:number,height:number,fontSize:number):Promise<string>;
    SetNativeTerminalBounds?(id:string,x:number,y:number,width:number,height:number):Promise<void>;
    FocusNativeTerminal?(id:string):Promise<void>;
    ClaimNativeTerminalInput?(id:string):Promise<void>;
    WriteTerminal(id:string,data:string):Promise<void>;ResizeTerminal(id:string,cols:number,rows:number):Promise<void>;OpenLibrary(kind:string):Promise<void>;
    Detach(id:string):Promise<void>;TermScroll(id:string,rows:number):Promise<void>;ScrollScreen(id:string,rows:number):Promise<void>;Clipboard(text:string):Promise<boolean>;
    Request(action:string,payload:string):Promise<string>,OpenedFile():Promise<string>}}}}
}
export async function request<T>(action:string, payload=''):Promise<T> {
  return JSON.parse(await window.go.main.App.Request(action,payload))
}
export interface LibraryEntry {
  kind:string; id:string; version?:string; path:string; status?:'preparing'|'ready'|'invalid'
  diagnostics?:{file:string; message:string; code:string}[]
}
export interface LibrarySnapshot {
  entries:LibraryEntry[]|null
  malformed?:LibraryEntry[]
}
export interface WorkflowField {
  name:string; type:string; required:boolean; default?:unknown
  description?:string; placeholder?:string; enum?:unknown[]
  control?:'toggle'|'segmented'|'select'|'folder'|'array'|'repos'|'number'|'multiline'|'text'
}
export interface WorkflowSummary {
  id:string; version:string; digest:string; path:string; origin:string
  form:{fields:WorkflowField[]}
}
export interface RunWorkflowRequest {
  request_id:string; workflow_id:string; expected_digest:string; work_dir?:string; inputs:Record<string,unknown>
}
export interface RunWorkflowResponse {
  node_id:string; run_id:string; status:string
}
export interface FlowNode {
  id:string; name:string; kind:string; status:string; run_id:string; parent_id?:string; attempt_id?:string;
  detail?:string;
  wait?:{kind:string;ref?:string}
}
export interface Workspace {
  cursor:number; nodes:FlowNode[]|null; edges:{from_id:string;to_id:string;milestone?:string}[]|null;
  attempts:{id:string;node_id:string;number:number;status:string;started_at:number;finished_at?:number}[]|null
}
const kinds:Record<string,Kind>={workflow:'group',group:'group',session:'session',harness:'session',tool:'step',watch:'watch'}
const states:Record<string,State>={waiting_for_dependencies:'pending',ready:'pending',running:'running',
  waiting_for_input:'blocked',stopping:'stopped',succeeded:'done',failed:'failed',interrupted:'blocked',skipped:'stopped',cancelled:'stopped'}
export interface LiveSession {session_id:string;attempt_id:string;status:string;command:string;work_dir:string;child:{pid:number}}
export function graphOf(w:Workspace, live:LiveSession[] = []) {
  const attempts=w.attempts??[], nodes=w.nodes??[]
  const attemptNodes=new Map(attempts.map(a=>[a.id,a.node_id]))
  const attemptsByID=new Map(attempts.map(a=>[a.id,a]))
  const tracked=new Set(live.map(s=>s.attempt_id))
  const ended=(status:string)=>!['running','waiting_for_input','stopping'].includes(status)
  const sessions=[...live.flatMap(s=>{const nodeID=attemptNodes.get(s.attempt_id),attempt=attemptsByID.get(s.attempt_id);if(!nodeID||!attempt)return[];const agent=s.command.split('/').pop()??'harness';return[{id:s.session_id,attempt:s.attempt_id,node:nodeID,agent,mode:'pty' as const,dir:s.work_dir,pid:s.child?.pid,started:new Date(attempt.started_at/1e6).toISOString(),exit:ended(attempt.status)?0:undefined,tag:attempt.status}]}),...attempts.filter(a=>!tracked.has(a.id)).flatMap(a=>{const node=nodes.find(n=>n.id===a.node_id);if(!node||node.kind!=='session')return[];return[{id:`attempt:${a.id}`,attempt:a.id,node:a.node_id,agent:node.name||'harness',mode:'pty' as const,dir:'',started:new Date(a.started_at/1e6).toISOString(),exit:ended(a.status)?0:undefined,tag:a.status}]})]
  return fromSnapshot({seq:w.cursor,sessions,nodes:nodes.map(n=>({
    id:n.id,title:n.name||n.id,kind:kinds[n.kind]??'step',state:states[n.status]??'pending',
    label:n.kind+' · '+n.status.replaceAll('_',' '),parent:n.parent_id,detail:n.detail,created:'',updated:''})),
    edges:(w.edges??[]).map(e=>({from:e.from_id,to:e.to_id,reason:e.milestone}))})
}
