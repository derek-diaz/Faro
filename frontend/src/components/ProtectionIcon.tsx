import { Baby, BriefcaseBusiness, Cpu, Gamepad2, House, Laptop, Lightbulb, Shield, Smartphone, Tv, UserRound, Users } from "lucide-react";
import type { ProtectionIconKey } from "../api/client";

export const protectionIcons: { key: ProtectionIconKey; label: string; icon: typeof House }[] = [
  { key: "house", label: "Home", icon: House },
  { key: "users", label: "Family", icon: Users },
  { key: "baby", label: "Children", icon: Baby },
  { key: "guest", label: "Guests", icon: UserRound },
  { key: "tv", label: "TV", icon: Tv },
  { key: "gamepad", label: "Gaming", icon: Gamepad2 },
  { key: "smartphone", label: "Phone", icon: Smartphone },
  { key: "laptop", label: "Computer", icon: Laptop },
  { key: "briefcase", label: "Work", icon: BriefcaseBusiness },
  { key: "lightbulb", label: "Smart home", icon: Lightbulb },
  { key: "cpu", label: "Server", icon: Cpu },
  { key: "shield", label: "Protection", icon: Shield }
];

export function ProtectionIcon({ name, size = 20 }: { readonly name: ProtectionIconKey; readonly size?: number }) {
  const Icon = protectionIcons.find((item) => item.key === name)?.icon ?? Shield;
  return <Icon size={size} />;
}
