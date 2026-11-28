import { Copy } from "lucide-react";
import { useState } from "react";
import { copyText } from "../utils/clipboard";
import { Button } from "./ui/button";

export function DnsServerAddress({ address }: { readonly address: string }) {
  const [feedback, setFeedback] = useState("");

  async function copyAddress() {
    try {
      await copyText(address);
      setFeedback("Address copied.");
    } catch {
      setFeedback("Select the DNS address above and copy it manually.");
    }
  }

  return <div className="dns-address-display">
    <div>
      <span>DNS server</span>
      <strong>{address}</strong>
      <small>Port 53 · UDP and TCP</small>
    </div>
    <Button type="button" variant="outline" onClick={() => void copyAddress()}><Copy size={16} aria-hidden="true" />Copy address</Button>
    <span className="onboarding-copy-feedback" role="status">{feedback}</span>
  </div>;
}
