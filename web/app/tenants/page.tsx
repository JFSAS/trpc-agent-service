"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { ApiNotice, EmptyState, PageHeader, StatusBadge } from "../../components/ui";
import { controlApi, type Tenant } from "../../lib/control-api";

export default function MyTenantsPage(){const[items,setItems]=useState<Tenant[]>([]);const[error,setError]=useState<unknown>();useEffect(()=>{void controlApi.listMyTenants().then((r)=>setItems(r.tenants)).catch(setError)},[]);return <><PageHeader eyebrow="TENANT CONTEXT" title="我的租户" description="选择明确的租户上下文后管理成员。"/><ApiNotice error={error}/>{items.length===0?<div className="panel"><EmptyState detail="请联系 Platform Operator 为账号开通 Tenant Membership。" title="当前没有可访问的租户"/></div>:<div className="tenant-cards">{items.map((tenant)=><Link className="tenant-card" href={`/tenants/${encodeURIComponent(tenant.id)}`} key={tenant.id}><header><h2>{tenant.name}</h2><StatusBadge tone={tenant.role==="OWNER"?"blue":"gray"}>{tenant.role}</StatusBadge></header><p>{tenant.slug}</p><footer><span>{tenant.status}</span><span>管理成员 →</span></footer></Link>)}</div>}</>}
