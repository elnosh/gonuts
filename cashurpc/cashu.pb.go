// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        (unknown)
// source: cashu.proto

package cashurpc

import (
	_ "google.golang.org/genproto/googleapis/api/annotations"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

var File_cashu_proto protoreflect.FileDescriptor

var file_cashu_proto_rawDesc = []byte{
	0x0a, 0x0b, 0x63, 0x61, 0x73, 0x68, 0x75, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x08, 0x63,
	0x61, 0x73, 0x68, 0x75, 0x2e, 0x76, 0x31, 0x1a, 0x0a, 0x6b, 0x65, 0x79, 0x73, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x1a, 0x0a, 0x73, 0x77, 0x61, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a,
	0x0a, 0x6d, 0x69, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x0a, 0x6d, 0x65, 0x6c,
	0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x0b, 0x73, 0x74, 0x61, 0x74, 0x65, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x0a, 0x69, 0x6e, 0x66, 0x6f, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x1a, 0x1c, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x61, 0x6e, 0x6e,
	0x6f, 0x74, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x32, 0xe3,
	0x06, 0x0a, 0x04, 0x4d, 0x69, 0x6e, 0x74, 0x12, 0x35, 0x0a, 0x04, 0x4b, 0x65, 0x79, 0x73, 0x12,
	0x0c, 0x2e, 0x4b, 0x65, 0x79, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x0d, 0x2e,
	0x4b, 0x65, 0x79, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x10, 0x82, 0xd3,
	0xe4, 0x93, 0x02, 0x0a, 0x12, 0x08, 0x2f, 0x76, 0x31, 0x2f, 0x6b, 0x65, 0x79, 0x73, 0x12, 0x3b,
	0x0a, 0x07, 0x4b, 0x65, 0x79, 0x53, 0x65, 0x74, 0x73, 0x12, 0x0c, 0x2e, 0x4b, 0x65, 0x79, 0x73,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x0d, 0x2e, 0x4b, 0x65, 0x79, 0x73, 0x52, 0x65,
	0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x13, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x0d, 0x12, 0x0b,
	0x2f, 0x76, 0x31, 0x2f, 0x6b, 0x65, 0x79, 0x73, 0x65, 0x74, 0x73, 0x12, 0x35, 0x0a, 0x04, 0x53,
	0x77, 0x61, 0x70, 0x12, 0x0c, 0x2e, 0x53, 0x77, 0x61, 0x70, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x0d, 0x2e, 0x53, 0x77, 0x61, 0x70, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x10, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x0a, 0x22, 0x08, 0x2f, 0x76, 0x31, 0x2f, 0x73, 0x77,
	0x61, 0x70, 0x12, 0x5b, 0x0a, 0x09, 0x4d, 0x69, 0x6e, 0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x12,
	0x15, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x4d, 0x69, 0x6e, 0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x16, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x4d, 0x69, 0x6e,
	0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x1f,
	0x82, 0xd3, 0xe4, 0x93, 0x02, 0x19, 0x22, 0x17, 0x2f, 0x76, 0x31, 0x2f, 0x6d, 0x69, 0x6e, 0x74,
	0x2f, 0x71, 0x75, 0x6f, 0x74, 0x65, 0x2f, 0x7b, 0x6d, 0x65, 0x74, 0x68, 0x6f, 0x64, 0x7d, 0x12,
	0x6b, 0x0a, 0x0e, 0x4d, 0x69, 0x6e, 0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x53, 0x74, 0x61, 0x74,
	0x65, 0x12, 0x15, 0x2e, 0x47, 0x65, 0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x53, 0x74, 0x61, 0x74,
	0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x16, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x4d,
	0x69, 0x6e, 0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x2a, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x24, 0x22, 0x22, 0x2f, 0x76, 0x31, 0x2f, 0x6d, 0x69,
	0x6e, 0x74, 0x2f, 0x71, 0x75, 0x6f, 0x74, 0x65, 0x2f, 0x7b, 0x6d, 0x65, 0x74, 0x68, 0x6f, 0x64,
	0x7d, 0x2f, 0x7b, 0x71, 0x75, 0x6f, 0x74, 0x65, 0x5f, 0x69, 0x64, 0x7d, 0x12, 0x46, 0x0a, 0x04,
	0x4d, 0x69, 0x6e, 0x74, 0x12, 0x10, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x4d, 0x69, 0x6e, 0x74, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x11, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x4d, 0x69, 0x6e,
	0x74, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x19, 0x82, 0xd3, 0xe4, 0x93, 0x02,
	0x13, 0x22, 0x11, 0x2f, 0x76, 0x31, 0x2f, 0x6d, 0x69, 0x6e, 0x74, 0x2f, 0x7b, 0x6d, 0x65, 0x74,
	0x68, 0x6f, 0x64, 0x7d, 0x12, 0x5b, 0x0a, 0x09, 0x4d, 0x65, 0x6c, 0x74, 0x51, 0x75, 0x6f, 0x74,
	0x65, 0x12, 0x15, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x4d, 0x65, 0x6c, 0x74, 0x51, 0x75, 0x6f, 0x74,
	0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x16, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x4d,
	0x65, 0x6c, 0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x22, 0x1f, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x19, 0x22, 0x17, 0x2f, 0x76, 0x31, 0x2f, 0x6d, 0x65,
	0x6c, 0x74, 0x2f, 0x71, 0x75, 0x6f, 0x74, 0x65, 0x2f, 0x7b, 0x6d, 0x65, 0x74, 0x68, 0x6f, 0x64,
	0x7d, 0x12, 0x6b, 0x0a, 0x0e, 0x4d, 0x65, 0x6c, 0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x53, 0x74,
	0x61, 0x74, 0x65, 0x12, 0x15, 0x2e, 0x47, 0x65, 0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x53, 0x74,
	0x61, 0x74, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x16, 0x2e, 0x50, 0x6f, 0x73,
	0x74, 0x4d, 0x65, 0x6c, 0x74, 0x51, 0x75, 0x6f, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0x2a, 0x82, 0xd3, 0xe4, 0x93, 0x02, 0x24, 0x22, 0x22, 0x2f, 0x76, 0x31, 0x2f,
	0x6d, 0x65, 0x6c, 0x74, 0x2f, 0x71, 0x75, 0x6f, 0x74, 0x65, 0x2f, 0x7b, 0x6d, 0x65, 0x74, 0x68,
	0x6f, 0x64, 0x7d, 0x2f, 0x7b, 0x71, 0x75, 0x6f, 0x74, 0x65, 0x5f, 0x69, 0x64, 0x7d, 0x12, 0x46,
	0x0a, 0x04, 0x4d, 0x65, 0x6c, 0x74, 0x12, 0x10, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x4d, 0x65, 0x6c,
	0x74, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x11, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x4d,
	0x65, 0x6c, 0x74, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x19, 0x82, 0xd3, 0xe4,
	0x93, 0x02, 0x13, 0x22, 0x11, 0x2f, 0x76, 0x31, 0x2f, 0x6d, 0x65, 0x6c, 0x74, 0x2f, 0x7b, 0x6d,
	0x65, 0x74, 0x68, 0x6f, 0x64, 0x7d, 0x12, 0x35, 0x0a, 0x04, 0x49, 0x6e, 0x66, 0x6f, 0x12, 0x0c,
	0x2e, 0x49, 0x6e, 0x66, 0x6f, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x0d, 0x2e, 0x49,
	0x6e, 0x66, 0x6f, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x10, 0x82, 0xd3, 0xe4,
	0x93, 0x02, 0x0a, 0x22, 0x08, 0x2f, 0x76, 0x31, 0x2f, 0x69, 0x6e, 0x66, 0x6f, 0x12, 0x55, 0x0a,
	0x0a, 0x43, 0x68, 0x65, 0x63, 0x6b, 0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x16, 0x2e, 0x50, 0x6f,
	0x73, 0x74, 0x43, 0x68, 0x65, 0x63, 0x6b, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x71, 0x75,
	0x65, 0x73, 0x74, 0x1a, 0x17, 0x2e, 0x50, 0x6f, 0x73, 0x74, 0x43, 0x68, 0x65, 0x63, 0x6b, 0x53,
	0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x16, 0x82, 0xd3,
	0xe4, 0x93, 0x02, 0x10, 0x22, 0x0e, 0x2f, 0x76, 0x31, 0x2f, 0x63, 0x68, 0x65, 0x63, 0x6b, 0x73,
	0x74, 0x61, 0x74, 0x65, 0x42, 0x7e, 0x0a, 0x0c, 0x63, 0x6f, 0x6d, 0x2e, 0x63, 0x61, 0x73, 0x68,
	0x75, 0x2e, 0x76, 0x31, 0x42, 0x0a, 0x43, 0x61, 0x73, 0x68, 0x75, 0x50, 0x72, 0x6f, 0x74, 0x6f,
	0x50, 0x01, 0x5a, 0x21, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x65,
	0x6c, 0x6e, 0x6f, 0x73, 0x68, 0x2f, 0x67, 0x6f, 0x6e, 0x75, 0x74, 0x73, 0x2f, 0x63, 0x61, 0x73,
	0x68, 0x75, 0x72, 0x70, 0x63, 0xa2, 0x02, 0x03, 0x43, 0x58, 0x58, 0xaa, 0x02, 0x08, 0x43, 0x61,
	0x73, 0x68, 0x75, 0x2e, 0x56, 0x31, 0xca, 0x02, 0x08, 0x43, 0x61, 0x73, 0x68, 0x75, 0x5c, 0x56,
	0x31, 0xe2, 0x02, 0x14, 0x43, 0x61, 0x73, 0x68, 0x75, 0x5c, 0x56, 0x31, 0x5c, 0x47, 0x50, 0x42,
	0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x09, 0x43, 0x61, 0x73, 0x68, 0x75,
	0x3a, 0x3a, 0x56, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var file_cashu_proto_goTypes = []interface{}{
	(*KeysRequest)(nil),            // 0: KeysRequest
	(*SwapRequest)(nil),            // 1: SwapRequest
	(*PostMintQuoteRequest)(nil),   // 2: PostMintQuoteRequest
	(*GetQuoteStateRequest)(nil),   // 3: GetQuoteStateRequest
	(*PostMintRequest)(nil),        // 4: PostMintRequest
	(*PostMeltQuoteRequest)(nil),   // 5: PostMeltQuoteRequest
	(*PostMeltRequest)(nil),        // 6: PostMeltRequest
	(*InfoRequest)(nil),            // 7: InfoRequest
	(*PostCheckStateRequest)(nil),  // 8: PostCheckStateRequest
	(*KeysResponse)(nil),           // 9: KeysResponse
	(*SwapResponse)(nil),           // 10: SwapResponse
	(*PostMintQuoteResponse)(nil),  // 11: PostMintQuoteResponse
	(*PostMintResponse)(nil),       // 12: PostMintResponse
	(*PostMeltQuoteResponse)(nil),  // 13: PostMeltQuoteResponse
	(*PostMeltResponse)(nil),       // 14: PostMeltResponse
	(*InfoResponse)(nil),           // 15: InfoResponse
	(*PostCheckStateResponse)(nil), // 16: PostCheckStateResponse
}
var file_cashu_proto_depIdxs = []int32{
	0,  // 0: cashu.v1.Mint.Keys:input_type -> KeysRequest
	0,  // 1: cashu.v1.Mint.KeySets:input_type -> KeysRequest
	1,  // 2: cashu.v1.Mint.Swap:input_type -> SwapRequest
	2,  // 3: cashu.v1.Mint.MintQuote:input_type -> PostMintQuoteRequest
	3,  // 4: cashu.v1.Mint.MintQuoteState:input_type -> GetQuoteStateRequest
	4,  // 5: cashu.v1.Mint.Mint:input_type -> PostMintRequest
	5,  // 6: cashu.v1.Mint.MeltQuote:input_type -> PostMeltQuoteRequest
	3,  // 7: cashu.v1.Mint.MeltQuoteState:input_type -> GetQuoteStateRequest
	6,  // 8: cashu.v1.Mint.Melt:input_type -> PostMeltRequest
	7,  // 9: cashu.v1.Mint.Info:input_type -> InfoRequest
	8,  // 10: cashu.v1.Mint.CheckState:input_type -> PostCheckStateRequest
	9,  // 11: cashu.v1.Mint.Keys:output_type -> KeysResponse
	9,  // 12: cashu.v1.Mint.KeySets:output_type -> KeysResponse
	10, // 13: cashu.v1.Mint.Swap:output_type -> SwapResponse
	11, // 14: cashu.v1.Mint.MintQuote:output_type -> PostMintQuoteResponse
	11, // 15: cashu.v1.Mint.MintQuoteState:output_type -> PostMintQuoteResponse
	12, // 16: cashu.v1.Mint.Mint:output_type -> PostMintResponse
	13, // 17: cashu.v1.Mint.MeltQuote:output_type -> PostMeltQuoteResponse
	13, // 18: cashu.v1.Mint.MeltQuoteState:output_type -> PostMeltQuoteResponse
	14, // 19: cashu.v1.Mint.Melt:output_type -> PostMeltResponse
	15, // 20: cashu.v1.Mint.Info:output_type -> InfoResponse
	16, // 21: cashu.v1.Mint.CheckState:output_type -> PostCheckStateResponse
	11, // [11:22] is the sub-list for method output_type
	0,  // [0:11] is the sub-list for method input_type
	0,  // [0:0] is the sub-list for extension type_name
	0,  // [0:0] is the sub-list for extension extendee
	0,  // [0:0] is the sub-list for field type_name
}

func init() { file_cashu_proto_init() }
func file_cashu_proto_init() {
	if File_cashu_proto != nil {
		return
	}
	file_keys_proto_init()
	file_swap_proto_init()
	file_mint_proto_init()
	file_melt_proto_init()
	file_state_proto_init()
	file_info_proto_init()
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_cashu_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   0,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_cashu_proto_goTypes,
		DependencyIndexes: file_cashu_proto_depIdxs,
	}.Build()
	File_cashu_proto = out.File
	file_cashu_proto_rawDesc = nil
	file_cashu_proto_goTypes = nil
	file_cashu_proto_depIdxs = nil
}
