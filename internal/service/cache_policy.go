package service

import "time"

// CachePolicy centralizes runtime cache switches and TTLs.
type CachePolicy struct {
	Enabled               bool
	ProjectDetailEnabled  bool
	ProjectListEnabled    bool
	VersionListEnabled    bool
	FaultTreeGraphEnabled bool
	KnowledgeGraphEnabled bool

	ProjectDetailTTL    time.Duration
	ProjectListTTL      time.Duration
	ProjectListIndexTTL time.Duration
	VersionListTTL      time.Duration
	VersionListIndexTTL time.Duration
	FaultTreeGraphTTL   time.Duration
	KnowledgeGraphTTL   time.Duration
}

func (p CachePolicy) normalize() CachePolicy {
	if p.ProjectDetailTTL <= 0 {
		p.ProjectDetailTTL = 30 * time.Minute
	}
	if p.ProjectListTTL <= 0 {
		p.ProjectListTTL = 2 * time.Hour
	}
	if p.ProjectListIndexTTL <= 0 {
		p.ProjectListIndexTTL = p.ProjectListTTL + 10*time.Minute
	}
	if p.VersionListTTL <= 0 {
		p.VersionListTTL = 30 * time.Minute
	}
	if p.VersionListIndexTTL <= 0 {
		p.VersionListIndexTTL = p.VersionListTTL + 10*time.Minute
	}
	if p.FaultTreeGraphTTL <= 0 {
		p.FaultTreeGraphTTL = 30 * time.Minute
	}
	if p.KnowledgeGraphTTL <= 0 {
		p.KnowledgeGraphTTL = 30 * time.Minute
	}
	return p
}
