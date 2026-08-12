export type Model={id:string;name:string;kind:'huggingface'|'local';source:string;repository:string;revision:string;local_path:string;status:string;size_bytes:number;created_at:string;updated_at:string}
export type RuntimeStatus='stopped'|'starting'|'running'|'stopping'|'failed'
export type Runtime={status:RuntimeStatus;pid:number;started_at?:string;ready_at?:string;stopped_at?:string;exit_error?:string;exit_code?:number;logs:string[]}
export type RuntimeOptions={model:string;host:string;port:number;tensor_parallel:number;served_model_name:string;extra_args:{name:string;values:string[]}[]}
export type GPU={index:number;name:string;uuid:string;memory_total_mib:number;memory_used_mib:number}
export type DownloadTask={id:string;repository:string;destination:string;state:'pending'|'running'|'succeeded'|'failed'|'canceled';progress:number;logs:string[];error?:string;started_at?:string;finished_at?:string}
export type Scope='inference'|'admin.read'|'admin.write'|'mcp.read'|'mcp.runtime'|'mcp.models'|'mcp.admin'
export type APIKey={id:string;name:string;prefix:string;enabled:boolean;scopes:Scope[];created_at:string;last_used_at?:string}
export type RequestMetadata={request_id:string;method:string;path:string;model:string;key_id:string;remote_addr:string;status_code:number;duration_ms:number;created_at:string}
export type MCPRequest={at:string;method?:string;name?:string;key_id?:string;remote_addr?:string;status_code:number;duration_ms:number}
export type MCPStatus={protocol_version:string;transport:string;stateless:boolean;recent_requests:MCPRequest[]}
export type Setting={key:string;value?:string;secret:boolean;updated_at:string}
export type SystemStatus={go_version:string;goos:string;goarch:string;cpus:number}
export type Dashboard={models:number;runtime:Runtime;downloads:DownloadTask[];recent_requests:RequestMetadata[]}
export class APIError extends Error{constructor(readonly status:number,message:string){super(message)}}
export const tokenStore={get:()=>sessionStorage.getItem('vllm-use-admin-token')??'',set:(v:string)=>v?sessionStorage.setItem('vllm-use-admin-token',v):sessionStorage.removeItem('vllm-use-admin-token')}
export async function api<T>(path:string,init?:RequestInit):Promise<T>{
 const headers=new Headers(init?.headers); headers.set('Accept','application/json'); const token=tokenStore.get(); if(token)headers.set('Authorization',`Bearer ${token}`); if(init?.body)headers.set('Content-Type','application/json')
 const response=await fetch(path,{...init,headers}); const body:unknown=await response.json().catch(()=>null)
 if(!response.ok){const message=typeof body==='object'&&body!==null&&'error' in body&&typeof body.error==='string'?body.error:`请求失败 (${response.status})`;throw new APIError(response.status,message)}
 return body as T
}
export const json=(value:unknown)=>JSON.stringify(value)
