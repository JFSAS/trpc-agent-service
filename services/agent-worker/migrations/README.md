
`0021_tenant_usage_governance.sql` adds the compact V1 tenant reservation and
settlement ledger. All Worker replicas share it. Missing Provider usage creates
an explicit unknown settlement and continues to hold its reservation.
