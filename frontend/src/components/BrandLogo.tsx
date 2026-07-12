type BrandLogoProps = {
  className?: string;
};

export function BrandLogo({ className = "" }: BrandLogoProps) {
  return <img className={`brand-logo ${className}`.trim()} src="/logos/web/icon-192.png" alt="" aria-hidden="true" />;
}
