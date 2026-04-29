export function SiteFooter() {
  return (
    <footer className="bg-white border-t border-gray-200 mt-auto">
      <div className="max-w-6xl mx-auto px-6 py-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-sm text-gray-600">SWAMP - Software Assurance Marketplace</p>
        <div className="flex items-center gap-5">
          <a href="https://chtc.cs.wisc.edu/" target="_blank" rel="noopener noreferrer" aria-label="CHTC home page">
            <img src="/logos/CHTC_Logo_Full_Color.svg" alt="CHTC" className="h-8 w-auto" />
          </a>
          <a href="https://cdis.wisc.edu/" target="_blank" rel="noopener noreferrer" aria-label="UW-Madison CDIS home page">
            <img src="/logos/UW_Crest.svg" alt="UW-Madison CDIS" className="h-8 w-auto" />
          </a>
          <a href="https://morgridge.org/" target="_blank" rel="noopener noreferrer" aria-label="Morgridge home page">
            <img src="/logos/Morgridge_Logo.svg" alt="Morgridge" className="h-8 w-auto" />
          </a>
        </div>
      </div>
    </footer>
  );
}