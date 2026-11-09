package compute

import (
	"encoding/json"
	"net/netip"
)

// UnmarshalJSON derives PublicIP from this response's internal interfaces.
// Wire interface fields retain their original spelling and values.
func (s *Server) UnmarshalJSON(data []byte) error {
	type wireServer Server
	var decoded wireServer
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = Server(decoded)
	s.PublicIP = serverPublicIP(s.InternalInterfaces)
	return nil
}

func serverPublicIP(interfaces []NetworkInterface) string {
	var public netip.Addr
	for _, nic := range interfaces {
		if nic.FloatingIP == "" {
			continue
		}
		address, err := netip.ParseAddr(nic.FloatingIP)
		if err != nil || address.Zone() != "" {
			return ""
		}
		address = address.Unmap()
		if !address.IsGlobalUnicast() || address.IsPrivate() || (public.IsValid() && public != address) {
			return ""
		}
		public = address
	}
	if public.IsValid() {
		return public.String()
	}
	return ""
}
