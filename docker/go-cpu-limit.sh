# Go versions in the image can see the host CPU count inside a quota-limited container.
set_go_cpu_limit() {
  [[ -z "${GOMAXPROCS:-}" ]] || return 0
  local quota period cpus available
  if [[ -r /sys/fs/cgroup/cpu.max ]]; then
    read -r quota period < /sys/fs/cgroup/cpu.max || return 0
  elif [[ -r /sys/fs/cgroup/cpu/cpu.cfs_quota_us && -r /sys/fs/cgroup/cpu/cpu.cfs_period_us ]]; then
    read -r quota < /sys/fs/cgroup/cpu/cpu.cfs_quota_us || return 0
    read -r period < /sys/fs/cgroup/cpu/cpu.cfs_period_us || return 0
  else
    return 0
  fi
  # Unlimited quotas are "max" on cgroup v2 and -1 on v1.
  [[ $quota =~ ^[1-9][0-9]*$ && $period =~ ^[1-9][0-9]*$ ]] || return 0
  cpus=$(( (quota + period - 1) / period ))
  available=$(nproc)
  if (( cpus > available )); then
    cpus=$available
  fi
  export GOMAXPROCS=$cpus
}

set_go_cpu_limit
