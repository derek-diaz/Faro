import { Camera, Car, DoorOpen, Gamepad2, HardDrive, Laptop, Lightbulb, MonitorSmartphone, PawPrint, Printer, Router, Server, Smartphone, Snowflake, Speaker, Sun, Tablet, Tv } from "lucide-react";
import type { DeviceSummary } from "../api/client";

export function deviceTypeIcon(type: string, size = 20) {
  switch (type) {
    case "Apple TV":
    case "TV": return <Tv size={size} />;
    case "Apple Device":
    case "Android Device":
    case "Android Phone":
    case "Phone": return <Smartphone size={size} />;
    case "Tablet": return <Tablet size={size} />;
    case "Smart TV":
    case "Roku": return <Tv size={size} />;
    case "Xbox":
    case "PlayStation":
    case "Nintendo":
    case "Game console": return <Gamepad2 size={size} />;
    case "Tesla":
    case "Vehicle": return <Car size={size} />;
    case "Windows PC":
    case "Mac":
    case "Computer": return <Laptop size={size} />;
    case "Linux Server": return <Server size={size} />;
    case "NAS": return <HardDrive size={size} />;
    case "Server / NAS": return <HardDrive size={size} />;
    case "Router": return <Router size={size} />;
    case "Smart home": return <Lightbulb size={size} />;
    case "Printer": return <Printer size={size} />;
    case "Camera": return <Camera size={size} />;
    case "Speaker": return <Speaker size={size} />;
    case "Enphase Solar System": return <Sun size={size} />;
    case "Midea Smart Appliance": return <Snowflake size={size} />;
    case "Petlibro Smart Pet Device": return <PawPrint size={size} />;
    case "Eufy Security Device": return <Camera size={size} />;
    case "Aqara Smart Home Device": return <Lightbulb size={size} />;
    case "Tuya Smart Home Device": return <Lightbulb size={size} />;
    case "MyQ Garage Door Controller": return <DoorOpen size={size} />;
    case "UniFi Network Device": return <Router size={size} />;
    case "TP-Link Smart Home Device": return <Lightbulb size={size} />;
    case "Meta Hardware": return <MonitorSmartphone size={size} />;
    case "Neakasa Smart Pet Device": return <PawPrint size={size} />;
    case "Amazon Alexa Device": return <Speaker size={size} />;
    case "Philips Hue Bridge": return <Lightbulb size={size} />;
    default: return <MonitorSmartphone size={size} />;
  }
}

export function deviceDisplayName(device?: DeviceSummary | null) {
  return device?.display_name || device?.name || device?.client_ip || "";
}

export function formatLastSeen(timestamp?: string | null, includeDate = false) {
  if (!timestamp) return "Not seen yet";
  const value = new Date(timestamp);
  const today = new Date();
  if (includeDate || value.toDateString() !== today.toDateString()) {
    return value.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
  }
  return value.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
