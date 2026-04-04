# Kubernetes PDB Webhook: Stop IP Churn During Upgrades

## The Problem

Node upgrades evict pods. All pods on a node disappear at once, releasing their IPs. When they restart, they request new IPs—creating churn that exhausts your VPC subnet. Upgrades stall. Services go down.

Pod Disruption Budgets prevent this. But enforcing them across a cluster is tedious. Developers forget them. No visibility. No enforcement.

## The Solution

A Kubernetes webhook that:
- **Validates**: Rejects deployments without matching PDBs
- **Auto-creates**: PDBs from namespace labels
- **Auto-protects**: Triggers rolling restarts on existing workloads when labels are added
- **Locks enforcement**: Labels are immutable (can't be removed or modified)
- **Audits everything**: Structured logs show what's enforced, rejected, and who tried to change rules

## Deploy It

GitHub: **https://github.com/forvaidya/pod-disruption-budget-enforce**

Label a namespace. Watch enforcement activate. Check logs. Upgrade smoothly with predictable IP consumption.

Production-ready: 2+ replicas, high availability, RBAC, 16+ tests, 6 operational guides.

---

*Built with Claude Code (Anthropic's AI development tool).*
