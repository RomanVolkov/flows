import {request} from './workspace'
import type {Totals as Summary} from './usage'
import type {Day} from './chart'

interface Bucket {
 key:string; known_subtotal:number; uncached_input_tokens:number; output_tokens:number;
 cache_read_tokens:number; cache_write_tokens:number; requests:number
}
interface Report extends Bucket {
 by_model?:Bucket[]; reasoning_tokens:number; latest_context_tokens:number
}
function filter(since:string) {
 return JSON.stringify({from:since?String(BigInt(Date.parse(since))*1000000n):'',tz:Intl.DateTimeFormat().resolvedOptions().timeZone})
}
export async function Totals(since:string,_node:string):Promise<Summary>{
 const r=await request<Report>('usage',filter(since))
 return {requests:r.requests,models:Object.fromEntries((r.by_model??[]).map(b=>[b.key,b.known_subtotal])),
 usage:{input:r.uncached_input_tokens,output:r.output_tokens,cache_read:r.cache_read_tokens,
 cache_write:r.cache_write_tokens,requests:r.requests,context:r.latest_context_tokens,reasoning:r.reasoning_tokens,transcripts:0}}
}
export async function Daily(since:string):Promise<Day[]>{
 const rows=await request<(Bucket&{date:string;model:string})[]>('daily',filter(since))
 const days=new Map<string,Day>()
 for(const row of rows){
  const key=JSON.stringify([row.date,row.model])
  const day=days.get(key)??{day:row.date,model:row.model,tokens:0,requests:0}
  day.tokens+=row.known_subtotal
  day.requests+=row.requests
  days.set(key,day)
 }
 return [...days.values()].sort((a,b)=>a.day.localeCompare(b.day))
}
