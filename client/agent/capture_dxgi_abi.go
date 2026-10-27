package agent

import (
	"fmt"
	"image"
	"unsafe"
)

// DXGI / D3D11 ABI tanimlari (struct yerlesimi, vtable indisleri, HRESULT'lar).
//
// Bu dosya build etiketsizdir: Windows disinda da derlenir, boylece struct
// boyut/offset sabitleri ve HRESULT siniflandirmasi her platformda birim
// testlenir (capture_dxgi_abi_test.go). COM cagrilari capture_dxgi_windows.go'da.
//
// Vtable indisleri: IUnknown (0 QueryInterface, 1 AddRef, 2 Release), sonra
// kalitim sirasi ile metotlar. Kaynak: dxgi.h / dxgi1_2.h / d3d11.h bildirim sirasi.

// IDXGIFactory (IDXGIObject: SetPrivateData 3, SetPrivateDataInterface 4,
// GetPrivateData 5, GetParent 6).
const (
	vtFactoryEnumAdapters = 7 // IDXGIFactory::EnumAdapters(UINT, IDXGIAdapter**)

	vtAdapterEnumOutputs = 7 // IDXGIAdapter::EnumOutputs(UINT, IDXGIOutput**)

	vtOutputGetDesc = 7 // IDXGIOutput::GetDesc(DXGI_OUTPUT_DESC*)

	vtOutput1DuplicateOutput = 22 // IDXGIOutput1::DuplicateOutput(IUnknown*, IDXGIOutputDuplication**)

	// IDXGIOutputDuplication (IDXGIObject 3..6): GetDesc 7, AcquireNextFrame 8,
	// GetFrameDirtyRects 9, GetFrameMoveRects 10, GetFramePointerShape 11,
	// MapDesktopSurface 12, UnMapDesktopSurface 13, ReleaseFrame 14.
	vtDupGetDesc         = 7
	vtDupAcquireNext     = 8
	vtDupGetDirtyRects   = 9
	vtDupGetMoveRects    = 10
	vtDupReleaseFrame    = 14
	vtDeviceCreateTex2D  = 5  // ID3D11Device::CreateTexture2D
	vtCtxMap             = 14 // ID3D11DeviceContext::Map
	vtCtxUnmap           = 15 // ID3D11DeviceContext::Unmap
	vtCtxCopySubresource = 46 // ID3D11DeviceContext::CopySubresourceRegion
	vtCtxCopyResource    = 47 // ID3D11DeviceContext::CopyResource
	vtTex2DGetDesc       = 10 // ID3D11Texture2D::GetDesc

	vtQueryInterface = 0
	vtRelease        = 2
)

// Sabitler.
const (
	dxgiFormatB8G8R8A8 = 87 // DXGI_FORMAT_B8G8R8A8_UNORM

	d3d11SDKVersion       = 7
	d3d11CreateBGRA       = 0x20    // D3D11_CREATE_DEVICE_BGRA_SUPPORT
	d3dDriverUnknown      = 0       // adapter verilince
	d3dDriverWarp         = 5       // D3D_DRIVER_TYPE_WARP
	d3d11UsageStaging     = 3       // D3D11_USAGE_STAGING
	d3d11CPUAccessRead    = 0x20000 // D3D11_CPU_ACCESS_READ
	d3d11MapRead          = 1       // D3D11_MAP_READ
	dxgiRotationUnspec    = 0
	dxgiRotationIdentity  = 1
	dxgiOutputDescNameLen = 32
)

// HRESULT'lar.
const (
	hrDXGIInvalidCall         uint32 = 0x887A0001
	hrDXGIMoreData            uint32 = 0x887A0003
	hrDXGIUnsupported         uint32 = 0x887A0004
	hrDXGIDeviceRemoved       uint32 = 0x887A0005
	hrDXGIDeviceReset         uint32 = 0x887A0007
	hrDXGINotCurrentlyAvail   uint32 = 0x887A0022
	hrDXGIAccessLost          uint32 = 0x887A0026
	hrDXGIWaitTimeout         uint32 = 0x887A0027
	hrDXGISessionDisconnected uint32 = 0x887A0028
	hrAccessDenied            uint32 = 0x80070005
)

// dxgiRect, RECT (left, top, right, bottom).
type dxgiRect struct{ Left, Top, Right, Bottom int32 }

func (r dxgiRect) rect() image.Rectangle {
	return image.Rect(int(r.Left), int(r.Top), int(r.Right), int(r.Bottom))
}

// dxgiMoveRect, DXGI_OUTDUPL_MOVE_RECT: SourcePoint + DestinationRect (24 bayt).
type dxgiMoveRect struct {
	SrcX, SrcY int32
	Dst        dxgiRect
}

// dxgiOutputDesc, DXGI_OUTPUT_DESC (64-bit: 96 bayt; Monitor offset 88).
type dxgiOutputDesc struct {
	DeviceName         [dxgiOutputDescNameLen]uint16
	DesktopCoordinates dxgiRect
	AttachedToDesktop  int32
	Rotation           uint32
	Monitor            uintptr
}

// dxgiOutduplDesc, DXGI_OUTDUPL_DESC (36 bayt): DXGI_MODE_DESC (28) + Rotation + DesktopImageInSystemMemory.
type dxgiOutduplDesc struct {
	Width, Height      uint32
	RefreshNum         uint32
	RefreshDen         uint32
	Format             uint32
	ScanlineOrdering   uint32
	Scaling            uint32
	Rotation           uint32
	DesktopInSystemMem int32
}

// dxgiFrameInfo, DXGI_OUTDUPL_FRAME_INFO (48 bayt).
type dxgiFrameInfo struct {
	LastPresentTime           int64
	LastMouseUpdateTime       int64
	AccumulatedFrames         uint32
	RectsCoalesced            int32
	ProtectedContentMaskedOut int32
	PointerX, PointerY        int32
	PointerVisible            int32
	TotalMetadataBufferSize   uint32
	PointerShapeBufferSize    uint32
}

// d3dTex2DDesc, D3D11_TEXTURE2D_DESC (44 bayt).
type d3dTex2DDesc struct {
	Width, Height, MipLevels, ArraySize uint32
	Format                              uint32
	SampleCount, SampleQuality          uint32
	Usage                               uint32
	BindFlags, CPUAccessFlags, MiscFlag uint32
}

// d3dMapped, D3D11_MAPPED_SUBRESOURCE (64-bit: 16 bayt).
type d3dMapped struct {
	Data       unsafe.Pointer
	RowPitch   uint32
	DepthPitch uint32
}

// d3dBox, D3D11_BOX (24 bayt).
type d3dBox struct{ Left, Top, Front, Right, Bottom, Back uint32 }

// classifyHRESULT, DXGI HRESULT'unu yeniden kurulabilir (errDXGILost),
// desteklenmeyen (errDXGIUnsupported) ya da genel hataya cevirir. WAIT_TIMEOUT
// cagiran tarafindan once ayiklanir (hata degildir).
func classifyHRESULT(op string, hr uint32) error {
	switch hr {
	case hrDXGIAccessLost, hrDXGIDeviceRemoved, hrDXGIDeviceReset,
		hrDXGISessionDisconnected, hrDXGIInvalidCall, hrAccessDenied:
		return fmt.Errorf("%s: HRESULT 0x%08X: %w", op, hr, errDXGILost)
	case hrDXGIUnsupported, hrDXGINotCurrentlyAvail:
		return fmt.Errorf("%s: HRESULT 0x%08X: %w", op, hr, errDXGIUnsupported)
	}
	return fmt.Errorf("%s: HRESULT 0x%08X", op, hr)
}
