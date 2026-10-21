# WuKongIM chat lifecycle checkpoint

- schema_version: `wukongim/chat-lifecycle-report/v5`
- threshold_version: `wukongim/chat-lifecycle-thresholds/v1`
- design_profile: `chat-lifecycle-soak/2026-08-04`
- profile: `local`
- stage: `shakeout`
- kind: `qualification`
- verdict: ``

## Redacted structured evidence

```json
{
  "schema_version": "wukongim/chat-lifecycle-report/v5",
  "threshold_version": "wukongim/chat-lifecycle-thresholds/v1",
  "design_profile": "chat-lifecycle-soak/2026-08-04",
  "config_digest": "sha256:74b32f14efa950bb217de4aabdc525f7f17cc6b7e3cba1d1d7a4ec2f1d0ffb7c",
  "dataset_digest": "sha256:66a67f9b058168e76786ba850d64dffe8dff1499f90d579b55e4f542d5549d38",
  "thresholds": {
    "minimum_data_filesystem_bytes": 10000000000,
    "disk_safe_stop_free_percent": 5,
    "correctness": {
      "terminal_sends": 0,
      "activation_rejections": 0,
      "losses": 0,
      "duplicates": 0,
      "corruptions": 0,
      "sequence_regressions": 0,
      "overall_first_attempt_failure": {
        "max_failures": 1,
        "per_attempts": 10000,
        "operator": "\u003c"
      },
      "any_minute_first_attempt_failure": {
        "max_failures": 1,
        "per_attempts": 1000,
        "operator": "\u003c="
      }
    },
    "latency": {
      "hot_sendack": {
        "p99": 400000000,
        "p999": 1000000000
      },
      "cold": {
        "p99": 2000000000,
        "p999": 5000000000
      },
      "sync": {
        "p99": 1000000000,
        "p999": 3000000000
      },
      "single_anomaly": 10000000000,
      "sustained_breach_window": 300000000000
    },
    "resource": {
      "forced_gc_live_heap_growth_percent": 5,
      "forced_gc_live_heap_window": 21600000000000,
      "goroutine_growth_percent": 5,
      "goroutine_growth_window": 86400000000000,
      "host_cpu_percent": 90,
      "host_memory_percent": 85,
      "bounded_queue_percent": 80,
      "sustained_saturation_window": 900000000000,
      "minimum_load_filesystem_bytes": 10000000000,
      "prometheus_safe_stop_bytes": 140000000000
    },
    "cluster": {
      "health_poll_every": 5000000000,
      "unhealthy_fail_after": 30000000000,
      "max_hot_replica_lag_entries": 64,
      "leader_imbalance_percent": 20,
      "leader_imbalance_for": 600000000000
    },
    "timeline": {
      "warmup": 600000000000,
      "checkpoint": 1200000000000,
      "final": 1800000000000
    }
  },
  "profile": "local",
  "mode": "soak",
  "stage": "shakeout",
  "kind": "qualification",
  "final": false,
  "continue": true,
  "continuous": false,
  "fence": {
    "run_hash": "sha256:e5a62fe5c09fabf7d72d3a298bfdcf0bd334001f4eebf6971b1ded483ed3e9e9",
    "assignment_hash": "sha256:e985b855699a6773148bfd39378214e827b307e18c7144077bc982c8741f3c7e",
    "generation": 1
  },
  "window": {
    "start": "2026-10-21T07:36:51.219782Z",
    "warmup_end": "2026-10-21T07:46:51.219782Z",
    "qualification_at": "2026-10-21T07:56:51.219782Z",
    "final_at": "2026-10-21T08:06:51.219782Z",
    "end": "2026-10-21T07:56:51.257979Z",
    "elapsed": 1200031575958
  },
  "minimum_worker_uptime": 1215045052125,
  "topology": {
    "validated": true,
    "logical_slot_groups": 12,
    "hash_slots": 256,
    "slot_replicas": 3,
    "channel_replicas": 3
  },
  "worker_generations": [
    {
      "worker_index": 0,
      "generation": 1,
      "snapshot_sequence": 240,
      "phase": "running"
    },
    {
      "worker_index": 1,
      "generation": 1,
      "snapshot_sequence": 240,
      "phase": "running"
    },
    {
      "worker_index": 2,
      "generation": 1,
      "snapshot_sequence": 240,
      "phase": "running"
    }
  ],
  "sessions": {
    "target": 2500,
    "online": 2499,
    "starting": 0,
    "closing": 0,
    "traffic_ready": 2499,
    "planned_new": 2758,
    "planned_returning": 697,
    "completed_new": 3258,
    "completed_returning": 163,
    "expired": 8,
    "close_reasons": {
      "expired": 922,
      "heartbeat_failed": 0,
      "remote_terminal": 0,
      "read_failed": 0,
      "generation_stop": 0,
      "explicit_logout": 0,
      "transport_close_failed": 0
    }
  },
  "generated": {
    "primary": 600500,
    "person": 540450,
    "group": 60050,
    "canary": 20,
    "payload_bytes": 458109440
  },
  "messages": {
    "sent": 600018,
    "send_attempts": 600020,
    "first_attempts": 600018,
    "first_attempt_failures": 0,
    "send_acknowledged": 599999,
    "send_rejected": 0,
    "received": 774351,
    "receive_acknowledged": 774351,
    "receive_ack_failures": 0,
    "retry_attempts": 2,
    "terminal": 0,
    "terminal_reasons": {
      "retry_exhausted": {
        "total": 0,
        "attempt_timeout": 0,
        "local_admission": 0,
        "transport_error": 0,
        "retryable_sendack": 0,
        "unclassified": 0
      },
      "non_retriable": 0,
      "session_closed": 0
    },
    "losses": 0,
    "duplicates": 0,
    "corruptions": 0,
    "sequence_regressions": 0
  },
  "sync": {
    "completed_new": 3258,
    "completed_returning": 163,
    "factory_failed": 0,
    "factory_canceled": 0,
    "connect_started": 3421,
    "connect_completed": 3421,
    "connect_failed": 0,
    "connect_canceled": 0,
    "sync_started": 3421,
    "sync_completed": 3421,
    "sync_failed": 0,
    "sync_canceled": 0,
    "failures": 0,
    "connect_latency": {
      "count": 3421,
      "sum_nanos": 16964051050,
      "max_nanos": 38067916,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        533,
        620,
        1072,
        749,
        375,
        72,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0
      ]
    },
    "latency": {
      "count": 3421,
      "sum_nanos": 21241747891,
      "max_nanos": 358619500,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        2934,
        239,
        86,
        2,
        1,
        19,
        41,
        82,
        17,
        0,
        0,
        0,
        0,
        0,
        0
      ]
    },
    "thresholds": {
      "P99Limit": 1000000000,
      "P999Limit": 3000000000,
      "Count": 3421,
      "AboveP99": 0,
      "AboveP999": 0,
      "Above10Seconds": 0
    }
  },
  "correlation": {
    "pending_unfinished": 19,
    "outstanding": 0,
    "sampled": 7267,
    "delivered": 7267,
    "expired": 0,
    "duplicate_completions": 0,
    "conflicting_completions": 0,
    "unknown_acknowledgments": 0
  },
  "queues": {
    "work_current": 502,
    "deferred_group_routes": 0,
    "work_peak": 54486,
    "work_capacity": 114500,
    "retry_current": 0,
    "retry_peak": 1,
    "retry_capacity": 12288,
    "inflight_current": 19,
    "inflight_peak": 500,
    "inflight_capacity": 12288,
    "transport_current": 0,
    "transport_capacity": 319872,
    "transport_rejected": 2
  },
  "harness": {
    "failures": 0,
    "command_saturation": 0,
    "offered_underdelivery": 0,
    "planned_cancellations": 0,
    "lifecycle_approval_rejections": {
      "missing_timer": 0,
      "invalid_shape": 0,
      "timer_fence": 0,
      "activity_fence": 0,
      "invalidated": 0,
      "fence_exhausted": 0,
      "deadline": 0,
      "replay_expired": 0,
      "replay_mismatch": 0
    },
    "drain_timed_out": false,
    "unexpected_exit": false
  },
  "evidence_counts": [
    0,
    0,
    0,
    0,
    0
  ],
  "lifecycle": {
    "candidates": 0,
    "loaded": 0,
    "cold_eligible": 0,
    "reheated": 0,
    "completed": 0,
    "product_failures": 0,
    "product_failure_reasons": {
      "initial_load": 0,
      "runtime_state": 0,
      "role_disagreement": 0,
      "watermark_regression": 0,
      "continued_loading": 0,
      "premature_absence": 0,
      "reheat_timeout": 0,
      "partial_reheat": 0,
      "sequence_proof": 0,
      "unexpected_reload": 0,
      "control_transition": 0
    },
    "harness_failures": 0,
    "reheat_latency": {
      "count": 0,
      "sum_nanos": 0,
      "max_nanos": 0,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0
      ]
    }
  },
  "meta_create": {
    "expected_unique": 12860,
    "created": 13497,
    "already_existing": 0,
    "errors": 0,
    "external_demo_activity": 637,
    "checkpoints": 1,
    "expected_by_slot": [
      1078,
      1113,
      1121,
      1133,
      1037,
      1074,
      1110,
      955,
      1075,
      1090,
      1010,
      1064
    ],
    "created_by_slot": [
      1129,
      1169,
      1180,
      1184,
      1102,
      1135,
      1166,
      1012,
      1118,
      1155,
      1051,
      1096
    ],
    "already_existing_by_slot": [
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0
    ],
    "errors_by_slot": [
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0,
      0
    ]
  },
  "latency": {
    "hot_sendack": {
      "count": 542829,
      "sum_nanos": 31441333852446,
      "max_nanos": 663132291,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        0,
        0,
        0,
        85,
        10745,
        318565,
        130502,
        80097,
        2758,
        77,
        0,
        0,
        0,
        0,
        0
      ]
    },
    "cold_first_create_sendack": {
      "count": 57170,
      "sum_nanos": 8769057216037,
      "max_nanos": 803085375,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        0,
        0,
        0,
        0,
        270,
        9173,
        9707,
        24961,
        12191,
        868,
        0,
        0,
        0,
        0,
        0
      ]
    },
    "worker_lifecycle_reheat_sendack": {
      "count": 0,
      "sum_nanos": 0,
      "max_nanos": 0,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0
      ]
    },
    "receive_ack": {
      "count": 774351,
      "sum_nanos": 12058744594,
      "max_nanos": 4089500,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        774337,
        8,
        6,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0
      ]
    },
    "send_pending_to_write": {
      "count": 599999,
      "sum_nanos": 687923819893,
      "max_nanos": 18633500,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        491,
        598258,
        1092,
        130,
        28,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0,
        0
      ]
    },
    "send_write_to_ack": {
      "count": 599999,
      "sum_nanos": 39519231394984,
      "max_nanos": 801892375,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        0,
        0,
        0,
        150,
        20684,
        322001,
        139138,
        102362,
        14753,
        911,
        0,
        0,
        0,
        0,
        0
      ]
    },
    "full_sync": {
      "count": 3421,
      "sum_nanos": 21241747891,
      "max_nanos": 358619500,
      "bucket_upper_nanos": [
        0,
        1000000,
        2000000,
        5000000,
        10000000,
        20000000,
        50000000,
        100000000,
        200000000,
        400000000,
        1000000000,
        2000000000,
        5000000000,
        10000000000,
        30000000000,
        60000000000
      ],
      "buckets": [
        0,
        2934,
        239,
        86,
        2,
        1,
        19,
        41,
        82,
        17,
        0,
        0,
        0,
        0,
        0,
        0
      ]
    },
    "warnings": {
      "hot": 0,
      "cold": 0,
      "sync": 0
    },
    "anomaly_count": 0,
    "retention": {
      "minute_samples": 12,
      "minute_capacity": 16,
      "latency_samples": [
        61,
        61,
        61
      ],
      "latency_capacity": 64,
      "heap_samples": [
        0,
        0,
        0
      ],
      "heap_capacity": 7,
      "goroutine_samples": [
        0,
        0,
        0
      ],
      "goroutine_capacity": 25
    }
  },
  "resources": {
    "nodes": [
      {
        "data_filesystem_bytes": 994662584320,
        "data_filesystem_available_bytes": 773924704256,
        "forced_gc_samples": 1,
        "heap_start_bytes": 137468792,
        "heap_end_bytes": 137468792,
        "goroutine_start": 2538,
        "goroutine_end": 2538,
        "sched_latency_mean_nanos": 1118,
        "sched_latency_mean_peak_nanos": 6160,
        "queue_baseline": 178,
        "queue_current": 36,
        "inflight_baseline": 198,
        "inflight_current": 25
      },
      {
        "data_filesystem_bytes": 994662584320,
        "data_filesystem_available_bytes": 773924704256,
        "forced_gc_samples": 1,
        "heap_start_bytes": 131936752,
        "heap_end_bytes": 131936752,
        "goroutine_start": 2492,
        "goroutine_end": 2492,
        "sched_latency_mean_nanos": 1335,
        "sched_latency_mean_peak_nanos": 4276,
        "queue_baseline": 210,
        "queue_current": 38,
        "inflight_baseline": 199,
        "inflight_current": 30
      },
      {
        "data_filesystem_bytes": 994662584320,
        "data_filesystem_available_bytes": 773924704256,
        "forced_gc_samples": 1,
        "heap_start_bytes": 133990336,
        "heap_end_bytes": 133990336,
        "goroutine_start": 2527,
        "goroutine_end": 2527,
        "sched_latency_mean_nanos": 2309,
        "sched_latency_mean_peak_nanos": 3943,
        "queue_baseline": 192,
        "queue_current": 43,
        "inflight_baseline": 185,
        "inflight_current": 33
      }
    ],
    "retention": {
      "minute_samples": 12,
      "minute_capacity": 16,
      "latency_samples": [
        61,
        61,
        61
      ],
      "latency_capacity": 64,
      "heap_samples": [
        0,
        0,
        0
      ],
      "heap_capacity": 7,
      "goroutine_samples": [
        0,
        0,
        0
      ],
      "goroutine_capacity": 25
    },
    "capacity": {
      "samples": 240,
      "missing_samples": 0,
      "sustained_window": 900000000000,
      "host_cpu_percent_basis_points": [
        3902,
        3902,
        3902,
        3902
      ],
      "host_memory_percent_basis_points": [
        6247,
        6247,
        6247,
        6247
      ],
      "service_queue_percent_basis_points": [
        49,
        49,
        59
      ],
      "cpu_high_samples": [
        0,
        1,
        0,
        0
      ],
      "memory_high_samples": [
        0,
        0,
        0,
        0
      ],
      "queue_high_samples": [
        0,
        0,
        0
      ],
      "cpu_sustained_events": [
        0,
        0,
        0,
        0
      ],
      "memory_sustained_events": [
        0,
        0,
        0,
        0
      ],
      "queue_sustained_events": [
        0,
        0,
        0
      ],
      "cpu_sustained_active": [
        false,
        false,
        false,
        false
      ],
      "memory_sustained_active": [
        false,
        false,
        false,
        false
      ],
      "queue_sustained_active": [
        false,
        false,
        false
      ],
      "worker_queue_samples": 240,
      "worker_queue_missing_samples": 0,
      "worker_queue_percent_basis_points": [
        [
          43,
          0,
          12,
          0
        ],
        [
          44,
          0,
          12,
          0
        ],
        [
          44,
          0,
          22,
          0
        ]
      ],
      "worker_queue_high_samples": [
        [
          0,
          0,
          0,
          0
        ],
        [
          0,
          0,
          0,
          0
        ],
        [
          0,
          0,
          0,
          0
        ]
      ],
      "worker_queue_sustained_events": [
        [
          0,
          0,
          0,
          0
        ],
        [
          0,
          0,
          0,
          0
        ],
        [
          0,
          0,
          0,
          0
        ]
      ],
      "worker_queue_sustained_active": [
        [
          false,
          false,
          false,
          false
        ],
        [
          false,
          false,
          false,
          false
        ],
        [
          false,
          false,
          false,
          false
        ]
      ],
      "worker_queues_complete": true,
      "data_filesystem_bytes": [
        994662584320,
        994662584320,
        994662584320,
        994662584320
      ],
      "data_filesystem_available_bytes": [
        773924704256,
        773924704256,
        773924704256,
        773924704256
      ],
      "system_filesystem_bytes": [
        994662584320,
        994662584320,
        994662584320,
        994662584320
      ],
      "system_filesystem_available_bytes": [
        773924704256,
        773924704256,
        773924704256,
        773924704256
      ],
      "prometheus_bytes": 37738,
      "network_transmit_bytes": 0,
      "process_up": [
        [
          true,
          true,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false
        ],
        [
          true,
          true,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false
        ],
        [
          true,
          true,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false,
          false
        ],
        [
          false,
          true,
          true,
          true,
          true,
          true,
          false,
          false,
          false,
          false,
          false,
          false,
          false
        ]
      ],
      "process_cpu_jiffies": [
        [
          63234,
          48,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0
        ],
        [
          62470,
          25,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0
        ],
        [
          62652,
          26,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0
        ],
        [
          0,
          26,
          5367,
          5354,
          5362,
          629,
          0,
          0,
          0,
          0,
          0,
          0,
          0
        ]
      ],
      "process_resident_memory_bytes": [
        [
          985759744,
          29720576,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0
        ],
        [
          939917312,
          29523968,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0
        ],
        [
          969129984,
          29032448,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0,
          0
        ],
        [
          0,
          29671424,
          328302592,
          325419008,
          329465856,
          55099392,
          0,
          0,
          0,
          0,
          0,
          0,
          0
        ]
      ],
      "processes_complete": true,
      "accrued_cost_micros": 0,
      "lease_remaining_seconds": 0,
      "complete": true
    }
  },
  "cluster": {
    "healthy_samples": 240,
    "unhealthy_samples": 0,
    "logical_slot_groups": 12,
    "leader_groups": 12,
    "full_replica_groups": 12,
    "hot_replica_lag_breaches": 0,
    "leader_imbalance_warnings": 0
  },
  "verdict": {
    "terminal": false,
    "cleanup_error_count": 0,
    "latency_warnings": {
      "hot": 0,
      "cold": 0,
      "sync": 0
    },
    "latency_anomaly_count": 0,
    "retention": {
      "minute_samples": 12,
      "minute_capacity": 16,
      "latency_samples": [
        61,
        61,
        61
      ],
      "latency_capacity": 64,
      "heap_samples": [
        0,
        0,
        0
      ],
      "heap_capacity": 7,
      "goroutine_samples": [
        0,
        0,
        0
      ],
      "goroutine_capacity": 25
    }
  },
  "capacity": {
    "attempted": false,
    "completed": false,
    "lower_bound": false,
    "maximum_passing_rate": 0,
    "first_failing_rate": 0,
    "recovery_passed": false
  }
}
```
